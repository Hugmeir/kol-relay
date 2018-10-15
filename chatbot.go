package main
import (
    "errors"
    "os"
    "os/signal"
    "sync"
    "strconv"
    "bytes"
    "fmt"
    "time"
    "syscall"

    "flag"
    "io/ioutil"
    "encoding/json"

    "math/rand"

    "database/sql"
    _ "github.com/mattn/go-sqlite3"
    "github.com/bwmarrin/discordgo"
    "github.com/Hugmeir/kolgo"
)

var prodMode = true
func RunningInDevMode() bool {
    return !prodMode
}

type ExtraDiscordData struct {
    RankIDToName   map[string]string
    Administrators map[string]bool
    Moderators     map[string]bool
}

type Chatbot struct {
    KoL     kolgo.KoLRelay
    Discord *discordgo.Session

    DiscordExtra        *ExtraDiscordData
    NameOverride        sync.Map
    GrumbledAt          sync.Map
    VerificationPending sync.Map
    SinceAPI            sync.Map
    HoldConsultsFor     sync.Map

    Inventory      KoLInventory
    InventoryMutex sync.Mutex

    eligibleStashMutex  sync.Mutex
    eligibleStashItems  []*kolgo.Item

    GlobalStfu  bool
    PartialStfu bool

    KillFile string

    Db *sql.DB
}

const killFile = "/tmp/kol-relay-KILL"
func init() {
    rand.Seed(time.Now().UnixNano())

    if _, err := os.Stat(killFile); !os.IsNotExist(err) {
        panic(errors.New("Killfile exists, refusing to start"))
    }

    // Get all the config files from arguments
    flag.StringVar(&dbConfJson,      "db_conf",      "", "Path to the database config JSON file")
    flag.StringVar(&discordConfJson, "discord_conf", "", "Path to the discord config JSON file")
    flag.StringVar(&kolConfJson,     "kol_conf",     "", "Path to the KoL config JSON file")
    flag.StringVar(&relayConfJson,   "relay_conf",   "", "Path to the relay targets JSON file")
    flag.StringVar(&toilConfJson,    "toil_conf",    "", "Path to the KoL config JSON for the clan management bot")
    flag.BoolVar(  &prodMode,        "wet_run",   false, "Running in production environment")

    flag.StringVar(&googleConfDir,    "google_conf_dir",    "", "Path to a dir holding credentials.json, token.json, and sheets.json")

    flag.Parse()

    // Open and read all of them; failing to read any is a panic
    _ = DbConf()
    _ = GetKoLConf()
    _ = GetDiscordConf()
    _ = GetRelayConf()
    _ = GetToilConf()
    _ = GetGoogleSheetsConf()

    tryLynx = DetectLynx()
}

var dbConfJson, discordConfJson, kolConfJson, relayConfJson, toilConfJson, googleConfDir string

var googleConf *GoogleSheetsConfig
func GetGoogleSheetsConf() *GoogleSheetsConfig {
    if googleConf != nil {
        return googleConf
    }

    sheets_json, err := ioutil.ReadFile(googleConfDir + "/sheets.json")
    if err != nil {
        panic(err)
    }

    googleConf = new(GoogleSheetsConfig)
    var sheetsConf map[string]string
    err = json.Unmarshal(sheets_json, &sheetsConf)
    if err != nil {
        panic(err)
    }

    googleConf.CredentialsFile = googleConfDir + "/credentials.json"
    googleConf.TokenFile       = googleConfDir + "/token.json"
    googleConf.SpreadsheetId   = sheetsConf[`sheet_id`]
    googleConf.ReadRange       = sheetsConf[`range`]

    return googleConf
}

type toilBotConf struct {
    Username string `json:"username"`
    Password string `json:"password"`
}
var toilConf *toilBotConf
func GetToilConf() *toilBotConf {
    if toilConf != nil {
        return toilConf
    }

    contents, err := ioutil.ReadFile(toilConfJson)
    if err != nil {
        panic(err)
    }

    toilConf = new(toilBotConf)
    err = json.Unmarshal(contents, toilConf)
    if err != nil {
        panic(err)
    }

    return toilConf
}

type KoLConf struct {
    Username string `json:"username"`
    Password string `json:"password"`
}
var readKoLConf *KoLConf
func GetKoLConf() *KoLConf {
    if readKoLConf != nil {
        return readKoLConf
    }

    contents, err := ioutil.ReadFile(kolConfJson)
    if err != nil {
        panic(err)
    }

    readKoLConf = new(KoLConf)
    err = json.Unmarshal(contents, readKoLConf)
    if err != nil {
        panic(err)
    }

    return readKoLConf
}

type DiscordConf struct {
    DiscordApiKey  string   `json:"discord_api_key"`
    AdminRole      string   `json:"admin_role"`
    ModeratorRoles []string `json:"moderator_roles"`
    EffectTransform map[string]string `json:"effect_transforms"`
}
var readDiscordConf *DiscordConf
func GetDiscordConf() *DiscordConf {
    if readDiscordConf != nil {
        return readDiscordConf
    }

    contents, err := ioutil.ReadFile(discordConfJson)
    if err != nil {
        panic(err)
    }

    readDiscordConf = new(DiscordConf)
    err = json.Unmarshal(contents, readDiscordConf)
    if err != nil {
        panic(err)
    }

    return readDiscordConf
}

var readRelayConf map[string]map[string]string
func GetRelayConf() map[string]map[string]string {
    if len(readRelayConf) > 0 {
        return readRelayConf
    }

    contents, err := ioutil.ReadFile(relayConfJson)
    if err != nil {
        panic(err)
    }
    err = json.Unmarshal(contents, &readRelayConf)
    if err != nil {
        panic(err)
    }

    return readRelayConf
}

type dbConf struct {
    DriverName string `json:"driver_name"`
    DataSource string `json:"data_source"`
}
var cachedDbConf *dbConf
func DbConf() *dbConf {
    if cachedDbConf != nil {
        return cachedDbConf
    }

    contents, err := ioutil.ReadFile(dbConfJson)
    if err != nil {
        panic(err)
    }

    cachedDbConf = new(dbConf)
    err = json.Unmarshal(contents, cachedDbConf)
    if err != nil {
        panic(err)
    }

    return cachedDbConf
}

func (bot *Chatbot)FleshenSQLData() {
    db := bot.Db

    var wg sync.WaitGroup

    // Do a query to fleshen all the name overrides
    wg.Add(1)
    go func() {
        defer wg.Done()
        rows, err := db.Query("SELECT discord_id, nickname FROM discord_name_override")
        if err != nil {
            panic(err)
        }
        defer rows.Close()
        for rows.Next() {
            var discordId string
            var nickname  string
            err = rows.Scan(&discordId, &nickname)
            if err != nil {
                fmt.Println(err)
                continue
            }
            bot.NameOverride.Store(discordId, nickname)
        }
        err = rows.Err()
        if err != nil {
            panic(err)
        }
    }()

    // Also do a query to fleshen the "your username & nickname differ, let me tell you why that sucks" list
    wg.Add(1)
    go func() {
        defer wg.Done()
        rows, err := db.Query("SELECT discord_id FROM discord_name_differs")
        if err != nil {
            panic(err)
        }
        defer rows.Close()
        for rows.Next() {
            var discordId string
            err = rows.Scan(&discordId)
            if err != nil {
                fmt.Println(err)
                continue
            }
            bot.GrumbledAt.Store(discordId, true)
        }
        err = rows.Err()
        if err != nil {
            panic(err)
        }
    }()

    wg.Wait()
}

func (bot *Chatbot)RelayToDiscord(destChannel string, toDiscord string) {
    if bot.GlobalStfu {
        return
    }
    bot.Discord.ChannelTyping(destChannel)
    go func() {
        time.Sleep(200 * time.Millisecond)
        bot.Discord.ChannelMessageSend(destChannel, toDiscord)
    }()
}

func NewDiscordConnection(botAPIKey string) *discordgo.Session {
    dg, err := discordgo.New("Bot " + botAPIKey)

    // Absurdly long timeout for connect:
    orig := dg.Client.Timeout
    dg.Client.Timeout = 1 * time.Minute
    defer func(){ dg.Client.Timeout = orig }()
    // Discord had a minor outage recently where new connections
    // took much longer than usual to stablish.
    // Discordgo has a default timeout of 20s, which is an
    // eternity already, but bump it to 1m during the initial
    // connection.

    err = dg.Open()
    if err != nil {
        panic(err)
    }

    return dg
}

var sqliteInsert sync.Mutex
func (bot *Chatbot)InsertNewNickname(discordId string, nick string) {
    sqliteInsert.Lock()
    defer sqliteInsert.Unlock()
    db := bot.Db

    now := time.Now().Format(time.RFC3339)
    stmt, err := db.Prepare("update discord_name_override set nickname=?, row_updated_at=? WHERE discord_id=?")
    if err != nil {
        fmt.Println("Entirely saved to save details for ", discordId, nick)
        return
    }
    defer stmt.Close()
    res, err := stmt.Exec(nick, now, discordId)
    if err == nil {
        affected, err := res.RowsAffected()
        if err != nil {
            fmt.Println("Error when updating an existing row, almost certainly worth ignoring: %s", err)
        } else if  affected > 0 {
            // This was an update!
            return
        }
    }

    stmt, err = db.Prepare("insert into discord_name_override (`discord_id`, `nickname`, `row_created_at`, `row_updated_at`) values (?, ?, ?, ?)")
    if err != nil {
        fmt.Println("Entirely saved to save details for ", discordId, nick)
        return
    }
    defer stmt.Close()
    res, err = stmt.Exec(discordId, nick, now, now)
    if err != nil {
        fmt.Println("Entirely failed to save details for ", discordId, nick, err)
        return
    }

    affected, err := res.RowsAffected()
    if err != nil {
        fmt.Printf("Error when getting rows affected for %s (%s): %s", nick, discordId, err)
    }

    if affected < 1 {
        fmt.Println("Could not insert new row for %s (%s) ", nick, discordId)
    }
}



/*
{"msgs":[{"msg":"A new trivial update has been posted: You can now walk away from the intro choice in the Neverending Party if you want, like if you accidentally show up wearing the wrong shirt or something.","type":"system","mid":"1468408333","who":{"name":"System Message","id":"-1","color":""},"format":"2","channelcolor":"green","time":"1537455943"}],"last":"1468408333","delay":3000}
*/
func (bot *Chatbot)HandleKoLSystemMessage(message kolgo.ChatMessage) (string, error) {
    msg := EscapeDiscordMetaCharacters(message.Msg)
    toDiscord := fmt.Sprintf("```css\n%s: %s\n```", message.Who.Name, msg)
    return toDiscord, nil
}

const (
    bruisedJaw  = "697"
    snowBall    = "718"
    unmotivated = "795"
)

type Effect struct {
    ID     string
    Name   string
    Turns  int
    Source string
}

func (bot *Chatbot)Uneffect(e *Effect) (bool, error) {
    kol := bot.KoL
    body, err := kol.Uneffect(e.ID)
    if err != nil {
        return false, err
    }

    if bytes.Contains(body, []byte("Effect removed.")) {
        return true, nil
    }

    if bytes.Contains(body, []byte(e.Name + " (")) {
        // Huh.  Still got it?  Maybe we ran out of SGEAs
        return false, nil
    }
    // Turns out we never had it!
    return false, nil
}


func (bot *Chatbot)FleshenAdministrators(defaultDiscordChannel string, discordConf *DiscordConf) {
    s := bot.Discord
    discordAdminRole, discordModeratorRoles := discordConf.AdminRole, discordConf.ModeratorRoles

    c, err := s.Channel(defaultDiscordChannel)
    if err != nil {
        fmt.Println("Could not resolve the default channel?!")
        return
    }

    g, err := s.Guild(c.GuildID)
    if err != nil {
        fmt.Println("Could not resolve the default guild?!")
        return
    }
    guildRoles := g.Roles

    if len(guildRoles) < 1 {
        guildRoles, err = s.GuildRoles(c.GuildID)
        if err != nil {
            fmt.Println("Could not get the guild roles")
            return
        }
    }

    moderatorRoles := map [string]bool{}
    for _, role := range discordModeratorRoles {
        moderatorRoles[role] = true
    }

    adminRole := map[string]bool{}
    moderatorRole := map[string]bool{}
    for _, r := range guildRoles {
        bot.DiscordExtra.RankIDToName["<@&" + r.ID + ">"] = r.Name

        if r.Name == discordAdminRole {
            adminRole[r.ID] = true
        }
        if moderatorRoles[r.Name] {
            moderatorRole[r.ID] = true
        }
    }

    for _, member := range g.Members {
        for _, roleName := range member.Roles {
            if _, ok := adminRole[roleName]; ok {
                bot.DiscordExtra.Administrators[member.User.ID] = true
                bot.DiscordExtra.Moderators[member.User.ID] = true
            } else if _, ok := moderatorRole[roleName]; ok {
                bot.DiscordExtra.Moderators[member.User.ID] = true
            }
        }
    }
}

func (bot *Chatbot)Cleanup() {
    // Close down the Discord session.
    defer bot.Discord.Close()

    // Disconnect from KoL
    defer bot.KoL.LogOut()

    // Disconnect from SQLite
    defer bot.Db.Close()
}

func NewChatbot(discordConf *DiscordConf, defaultDiscordChannel string, kolConf *KoLConf, fromKoL *os.File) *Chatbot {
    var wg sync.WaitGroup
    // Connect to discord
    wg.Add(3)
    var dg *discordgo.Session
    var kol kolgo.KoLRelay
    var db *sql.DB
    go func() {
        defer wg.Done()
        dg = NewDiscordConnection(discordConf.DiscordApiKey)
    }()

    // Conenct to KoL
    go func() {
        defer wg.Done()
        kol = kolgo.NewKoL(kolConf.Username +"/q", kolConf.Password, fromKoL)
        err := kol.LogIn()
        if err != nil {
            panic(err)
        }
    }()

    go func() {
        defer wg.Done()
        dbConf := DbConf()
        var err error
        db, err = sql.Open(dbConf.DriverName, dbConf.DataSource)
        if err != nil {
            panic(err)
        }
        err = db.Ping()
        if err != nil {
            panic(err)
        }
        // Nice, sqlite works
    }()

    wg.Wait()

    bot := &Chatbot{
        KoL:         kol,
        Discord:     dg,
        GlobalStfu:  false,
        PartialStfu: false,
        KillFile:    killFile,
        Db:          db,
        DiscordExtra: &ExtraDiscordData{
            make(map[string]string, 20),
            make(map[string]bool, 20),
            make(map[string]bool, 20),
        },
    }

    wg.Add(3)

    go func() {
        defer wg.Done()
        bot.RefreshInventory()
    }()

    go func() {
        defer wg.Done()
        bot.FleshenSQLData()
    }()

    go func() {
        defer wg.Done()
        bot.FleshenAdministrators(defaultDiscordChannel, discordConf)
    }()
    wg.Wait()

    return bot
}

const (
    EffectName   int = iota
    EffectTurns
    EffectImg
    EffectSource
    EffectID
)
type APIStatus struct {
    ID          string              `json:"playerid"`
    Name        string              `json:"name"`
    Effects     map[string][]string `json:"effects"`
}
func DecodeStatusResponse(body []byte) *APIStatus {
    var st APIStatus
    err := json.Unmarshal(body, &st)
    if err != nil {
        return nil
    }
    return &st
}
func RawEffectToEffect(r []string) *Effect {
    turns, _ := strconv.Atoi(r[EffectTurns])
    return &Effect{
        ID:     r[EffectID],
        Name:   r[EffectName],
        Turns:  turns,
        Source: r[EffectSource],
    }
}

func (bot *Chatbot) ClearUnwantedEffects() {
    body, err := bot.KoL.APIRequest("status", nil)
    if err != nil {
        return
    }

    effects := []*Effect{}
    status  := DecodeStatusResponse(body)
    if status == nil {
        effects = append(effects, &Effect{
            ID:   bruisedJaw,
            Name: "Bruised Jaw",
        })
    } else {
        for _, r := range status.Effects {
            e := RawEffectToEffect(r)
            effects = append(effects, e)
        }
    }
    mustUneffect := map[string]bool{
        "697": true,
        "718": true,
        "795": true,
    }
    for _, e := range effects {
        if _, ok := mustUneffect[e.ID]; !ok {
            continue
        }

        cleared, _ := bot.Uneffect(e)
        if ! cleared && e.ID == bruisedJaw {
            fmt.Printf("Started with effect %s, and could not clear it!\n", e.Name)
            bot.PartialStfu = true
        }
    }
}

const (
    FORTUNE_REAL_CONSULT    = "real"
    FORTUNE_VIRTUAL_CONSULT = "virtual"
)

func (bot *Chatbot)RespondToOutstandingConsults() {
    // This is how we deal with "long term" holds:  Anything that is held when
    // we start-up is counted as long-term
    time.Sleep(1 * time.Minute)
    kol := bot.KoL
    b, err := kol.ClanVIPFortune()
    if err != nil {
        fmt.Println("Could not get outstanding consults: ", err)
        return
    }

    outstanding := kolgo.DecodeOutstandingZataraConsults(b)
    for _, p := range outstanding {
        bot.HoldConsultsFor.Store(p.ID, FORTUNE_REAL_CONSULT)
    }

    ticker := time.NewTicker(30 * time.Minute)
    for {select {
    case <-ticker.C:
        kol := bot.KoL
        b, err := kol.ClanVIPFortune()
        if err != nil {
            fmt.Println("Could not get consults: ", err)
            continue
        }

        outstanding := kolgo.DecodeOutstandingZataraConsults(b)
        for _, p := range outstanding {
            if _, ok := bot.HoldConsultsFor.Load(p.ID); ok {
                // Holding this consult
                continue
            }
            time.Sleep(5 * time.Second)
            _, _ = kol.ClanResponseLoveTest(p.ID, FCA_RESPONSE1, FCA_RESPONSE2, FCA_RESPONSE3)
        }
    }}
}

func main() {
    fromDiscordLogfile := "/var/log/kol-relay/relay.log"
    fromKoLLogfile     := "/var/log/kol-relay/from_kol.log"

    fromDiscord, err := os.OpenFile(fromDiscordLogfile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
    if err != nil {
        panic(err)
    }
    defer fromDiscord.Close()

    fromKoL, err := os.OpenFile(fromKoLLogfile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
    if err != nil {
        panic(err)
    }
    defer fromKoL.Close()

    relayConf                 := GetRelayConf()
    defaultDiscordChannel, ok := relayConf["from_kol_to_discord"]["clan"]
    if !ok {
        panic("The /clan channel MUST be part of the relays!")
    }

    discordConf := GetDiscordConf()
    kolConf     := GetKoLConf()

    bot := NewChatbot(discordConf, defaultDiscordChannel, kolConf, fromKoL)
    defer bot.Cleanup()

    // This handler is called when the bot sees a message on discord
    bot.Discord.AddHandler(func (s *discordgo.Session, m *discordgo.MessageCreate) {
        bot.HandleMessageFromDiscord(s, m, fromDiscord)
    })

    // Start the chat poller.
    go bot.KoL.StartChatPoll()
    go bot.KoL.StartMessagePoll()

    // Clear the Bruised Jaw effect.  If we fail, do not relay bessages
    // from discord into kol
    go bot.ClearUnwantedEffects()

    // Try sending the initial message to confirm that everything is working
    // NOTE: We use SubmitChat, not the "nicer" interface SendMessage here,
    // because want to die if this fails to send.
    _, err = bot.KoL.SubmitChat("/msg hugmeir", "oh hai creator")
    if err != nil {
        fmt.Println("Cannot send initial message, something has gone wrong: %v", err)
        panic(err)
    }

    // This handler is called when we see a "public" message in KoL chat -- a public
    // message is basically anything that is not a private message (/msg), and event
    // (like getting hit with a jawbruiser) or a system message (trivial announcements)
    bot.KoL.AddHandler(kolgo.Public, func (kol kolgo.KoLRelay, message kolgo.ChatMessage) {
        targetDiscordChannel, ok := relayConf["from_kol_to_discord"][message.Channel]
        if !ok {
            return
        }

        toDiscord, err := bot.HandleKoLPublicMessage(message, discordConf.EffectTransform)
        if err != nil {
            // TODO
            return
        }
        if toDiscord == "" {
            return
        }
        bot.RelayToDiscord(targetDiscordChannel, toDiscord)
    })

    // Called when we see a system message in KoL.  Currently untested because, well,
    // those are rare >.>
    bot.KoL.AddHandler(kolgo.System, func (kol kolgo.KoLRelay, message kolgo.ChatMessage) {
        toDiscord, err := bot.HandleKoLSystemMessage(message)
        if err != nil {
            // TODO
            return
        }
        if toDiscord == "" {
            return
        }
        bot.RelayToDiscord(defaultDiscordChannel, toDiscord)
    })

    // Called when we get a private message in KoL
    bot.KoL.AddHandler(kolgo.Private, func (kol kolgo.KoLRelay, message kolgo.ChatMessage) {
        toDiscord, err := bot.HandleKoLDM(message)
        if err != nil {
            // TODO
            return
        }
        if toDiscord == "" {
            return
        }
        bot.RelayToDiscord(defaultDiscordChannel, toDiscord)
    })

    // Called when we see an 'event', like getting jawbruised or snowballed
    bot.KoL.AddHandler(kolgo.Event, func (kol kolgo.KoLRelay, message kolgo.ChatMessage) {
        toDiscord, err := bot.HandleKoLEvent(message)
        if err != nil {
            // TODO
            return
        }
        if toDiscord == "" {
            return
        }
        bot.RelayToDiscord(defaultDiscordChannel, toDiscord)
    })

    googleSheetsConf := GetGoogleSheetsConf()
    toilbot := NewToilBot(toilConf.Username + "/q", toilConf.Password, bot.Db, googleSheetsConf)
    toilbot.MaintainBlacklist(bot)

    toilbot.AddHandler(AcceptedApplication, func (app ClanApplication) {
        announcement := fmt.Sprintf(FCA_AnnounceKoLFmt, app.PlayerName, app.PlayerID)
        // Nice, we got a new clannie.  Make relay send them the welcome kmail:
        bot.KoL.SendKMail(app.PlayerName, FCA_WELCOME, 0, nil)
        // And announce it in /clan & discord:
        bot.KoL.SendMessage("/clan", announcement)
        bot.Discord.ChannelMessageSend(defaultDiscordChannel, announcement)
    })
/*
    toilbot.AddHandler(RejectedApplication, func (app *ClanApplication) {
        // And announce it in /clan & discord:
        bot.Discord.ChannelMessageSend(announceChannel, announcement)
    })
*/
    go toilbot.PollClanManagement(bot)

    go bot.RespondToOutstandingConsults()

    fmt.Println("Bot is now running.  Press CTRL-C to exit.")
    sc := make(chan os.Signal, 1)
    signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
    <-sc
}


package main
import (
    "errors"
    "os"
    "os/signal"
    "sync"
    "strconv"
    "regexp"
    "fmt"
    "time"
    "flag"
    "syscall"

    "unicode"
    "unicode/utf8"
    "golang.org/x/text/unicode/norm"
    "golang.org/x/text/unicode/runenames"
    "golang.org/x/text/encoding/charmap"

    "io/ioutil"
    "strings"
    "math/rand"
    "encoding/json"
    "database/sql"
    _ "github.com/mattn/go-sqlite3"
    "github.com/bwmarrin/discordgo"
    "github.com/Hugmeir/kolgo"
)

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
    flag.Parse()

    // Open and read all of them; failing to read any is a panic
    _ = DbConf()
    _ = GetKoLConf()
    _ = GetDiscordConf()
    _ = GetRelayConf()
    _ = GetToilConf()

    tryLynx = DetectLynx()
}

var dbConfJson, discordConfJson, kolConfJson, relayConfJson, toilConfJson string

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

    err = dg.Open()
    if err != nil {
        panic(err)
    }

    return dg
}

func (bot *Chatbot)ResolveNickname(s *discordgo.Session, m *discordgo.MessageCreate) string {
    id := m.Author.ID

    result, ok := bot.NameOverride.Load(id)
    if ok {
        return result.(string)
    }

    go bot.GrumbleIfNicknameAndUsernameDiffer(s, m)

    return m.Author.Username
}

func (bot *Chatbot)InsertNicknameGrumble(discordId string) {
    // Put it in the in-memory cache first:
    bot.GrumbledAt.Store(discordId, true)

    sqliteInsert.Lock()
    defer sqliteInsert.Unlock()
    db := bot.Db

    now := time.Now().Format(time.RFC3339)
    stmt, err := db.Prepare("insert into discord_name_differs (`discord_id`, `row_created_at`, `row_updated_at`) values (?, ?, ?)")
    if err != nil {
        fmt.Println("Failed to retain that we already spammed", discordId, "so we will end up doing it again, reason:", err)
        return
    }
    defer stmt.Close()
    _, err = stmt.Exec(discordId, now, now)
    if err != nil {
        fmt.Println("Failed to retain that we already spammed", discordId, "so we will end up doing it again, reason:", err)
        return
    }

    return
}

func (bot *Chatbot)GrumbleIfNicknameAndUsernameDiffer(s *discordgo.Session, m *discordgo.MessageCreate) {
    _, ok := bot.GrumbledAt.Load(m.Author.ID)
    if ok {
        return
    }

    c, err := s.Channel(m.ChannelID)
    if err != nil {
        return
    }

    g, err := s.Guild(c.GuildID)
    if err != nil {
        return
    }

    for _, member := range g.Members {
        if m.Author.ID != member.User.ID {
            continue;
        }
        if member.Nick == "" {
            bot.GrumbledAt.Store(m.Author.ID, true)
            return // No nickname
        }
        nick := member.Nick
        if strings.EqualFold(nick, m.Author.Username) {
            bot.GrumbledAt.Store(m.Author.ID, true)
            return
        }

        userChannel, err := s.UserChannelCreate(m.Author.ID)
        if err != nil {
            fmt.Println("Could not ping someone about their username, error: ", err)
            return
        }

        // Okay, so their nickname differs from their username.  Have we poked them before?
        go bot.InsertNicknameGrumble(m.Author.ID)
        msg := fmt.Sprintf("To prevent abuse, the relay has to use your discord username (%s) when showing messages in KoL, not your nickname (%s).\n\nTo make it use your in-game name, send it a private message in-game with this: `/msg RelayBot Verify` and follow the instructions.", m.Author.Username, nick)
        s.ChannelMessageSend(userChannel.ID, msg)

        return
    }
}

const maxEmoji = 3
func EmojiNoMore(s string) string {
    seenEmoji := 0
    for i, w := 0, 0; i < len(s); i += w {
        c, width := utf8.DecodeRuneInString(s[i:])
        w = width
        if c > 0xFF && unicode.IsSymbol(c) {
            if seenEmoji++; seenEmoji > maxEmoji {
                return "[Bunch of emojis]"
            }
            name := runenames.Name(c)
            s = s[:i] + "[" + name + "]" + s[i+w:]
            w = len(name) + 2
        }
    }
    return s
}

// This should be [\p{Latin1}\p{ASCII}], but no such thing in golang
var nonLatin1Re = regexp.MustCompile(`[^\x00-\xff]`)
func ClearNonLatin1Characters(s string) string {
    // KoL chat only accepts the latin1 range:
    return nonLatin1Re.ReplaceAllString(s, ``)
}

func sanitizeForKoL (content string) string {

    // Remove smallcaps and similar deviants
    content = AsciiFold(content)

    // Try to convert emoji into their character names.  Makes for
    // weirder messages but...
    content = EmojiNoMore(content)

    // Try to normalize into NFC, so that combining characters
    // in the latin1 range look reasonable
    content = norm.NFC.String(content)

    content = ClearNonLatin1Characters(content)

    // KoL chat only accepts latin1, so encode before sending:
    encoded, err := charmap.ISO8859_1.NewEncoder().String(content)
    if err != nil {
        fmt.Printf("Failed to encode message: %v\n", err)
        encoded = content
    }

    return encoded
}

func (bot *Chatbot)IsAdminRole(s *discordgo.Session, m *discordgo.MessageCreate) bool {
    _, ok := bot.DiscordExtra.Administrators[m.Author.ID]
    if ok {
        return true
    }

    return false
}

func (bot *Chatbot)SenderCanRunCommands(s *discordgo.Session, m *discordgo.MessageCreate) bool {
    if bot.IsAdminRole(s, m) {
        return true
    }

    if m.Author.Username == "hugmeir" {
        return true
    }

    return false
}

func (bot *Chatbot)SenderIsModerator(s *discordgo.Session, m *discordgo.MessageCreate) bool {
    if bot.SenderCanRunCommands(s, m) {
        return true
    }

    _, ok := bot.DiscordExtra.Moderators[m.Author.ID]
    if ok {
        return true
    }

    return false
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

func HandleAliasing(bot *Chatbot, s *discordgo.Session, m *discordgo.MessageCreate, matches []string) {
    discordName := matches[1]
    kolName     := matches[2]

    if !bot.SenderIsModerator(s, m) {
        s.ChannelMessageSend(m.ChannelID, "Naughty members will be reported")
        return
    }

    mentions := m.Mentions
    if len(mentions) != 1 {
        s.ChannelMessageSend(m.ChannelID, "Your alias command seems broken, no clue what you meant to do")
        return
    }

    discordID := mentions[0].ID
    fmt.Printf("'%s' asked us to alias '%s' (id %s) to '%s'\n", m.Author.ID, discordName, discordID, kolName)

    go bot.InsertNewNickname(discordID, kolName)
    // Put in our in-memory hash:
    bot.NameOverride.Store(discordID, kolName)
    // Let 'em know:
    s.ChannelMessageSend(m.ChannelID, "Alias registered.  Reflect on your mistakes.")
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
    bruisedJaw = 697
    snowBall   = 718
)

func (bot *Chatbot)ClearJawBruiser() (bool, error) {
    kol := bot.KoL
    body, err := kol.Uneffect(strconv.Itoa(bruisedJaw))
    if err != nil {
        return false, err
    }

    bod := string(body)
    if strings.Contains(bod, "Effect removed.") {
        return true, nil
    }
    if strings.Contains(bod, "Bruised Jaw (") {
        fmt.Println("UNEFFECT FAILED: ", string(body))
        return false, nil
    }
    // Turns out we never had it!
    return true, nil
}

func (bot *Chatbot)ClearSnowball() (bool, error) {
    kol := bot.KoL
    body, err := kol.Uneffect(strconv.Itoa(snowBall))
    if err != nil {
        return false, err
    }

    bod := string(body)
    if strings.Contains(bod, "Effect removed.") {
        return true, nil
    }
    if strings.Contains(bod, "B-b-brr! (") {
        fmt.Println("UNEFFECT FAILED: ", string(body))
        return false, nil
    }
    // Turns out we never had it!
    return true, nil
}

type triggerTuple struct {
    re *regexp.Regexp
    cb func(*Chatbot, *discordgo.Session, *discordgo.MessageCreate, []string)
}

var discordMessageTriggers = []triggerTuple {
    triggerTuple {
        regexp.MustCompile(`(?i)/whois\s+hugmeir`),
        func(bot *Chatbot, s *discordgo.Session, m *discordgo.MessageCreate, matches []string) {
            if m.Author.Username == "hugmeir" {
                return
            }
            s.ChannelMessageSend(m.ChannelID, "Ssh! Don't summon the accursed creator!")
        },
    },
    triggerTuple {
        regexp.MustCompile(`(?i)^\s*Relay(?:Bot)?,?\s+tell\s+me\s+about\s+(.+)`),
        func(bot *Chatbot, s *discordgo.Session, m *discordgo.MessageCreate, matches []string) {
            about := matches[1]
            if strings.Contains(about, "IO ERROR") {
                s.ChannelMessageSend(m.ChannelID, "Still doing that, are you?  You'll want to talk to one of _those_ other bots")
            } else if strings.Contains(about, "verification") {
                msg := "The relay uses your discord username.  To make it use your in-game name, `/msg RelayBot Verify` in-game and follow the instructions"
                s.ChannelMessageSend(m.ChannelID, msg)
                bot.KoL.SendMessage(`/clan`, msg)
            }
        },
    },
    triggerTuple {
        regexp.MustCompile(`(?i)\A!(?:cmd|powerword) (?:alias|verif(?:y|ication)) (<@![0-9]+>) (?:(?:as|to) )?(.+)\z`),
        HandleAliasing,
    },

    // ALWAYS LAST
    triggerTuple {
        regexp.MustCompile(`(?i)\A!(?:cmd|powerword)\s+([a-zA-Z0-9]*)`),
        func (bot *Chatbot, s *discordgo.Session, m *discordgo.MessageCreate, matches []string) {
            cmd := matches[1]
            if cmd == "" {
                cmd = "unknown"
            }
            s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Speak loudly dear, I don't know what this '%s' command means", cmd))
        },
    },
}

func (bot *Chatbot)DiscordMessageTriggers(s *discordgo.Session, m *discordgo.MessageCreate) {
    content := m.Content

    for _, trigger := range discordMessageTriggers {
        re      := trigger.re
        matches := re.FindStringSubmatch(content)
        if len(matches) == 0 {
            continue
        }
        trigger.cb(bot, s, m, matches)
        return
    }
}

/*
{"id":"493885813935964160","channel_id":"490888267550425123","content":"\u003c@\u0026472130126482505740\u003e \u003c@289897239579459584\u003e \u003c#49088
8267550425123\u003e \u003c:catplanet:493885480971141152\u003e","timestamp":"2018-09-24T20:45:53.997000+00:00","edited_timestamp":"","mention_roles":["472130126482505740"],"tts":fals
e,"mention_everyone":false,"author":{"id":"289897239579459584","email":"","username":"hugmeir","avatar":"","discriminator":"0463","token":"","verified":false,"mfa_enabled":false,"bo
t":false},"attachments":[],"embeds":[],"mentions":[{"id":"289897239579459584","email":"","username":"hugmeir","avatar":"","discriminator":"0463","token":"","verified":false,"mfa_ena
bled":false,"bot":false}],"reactions":null,"type":0}
*/
var extraUnhandledMentions = regexp.MustCompile(`(?i)<(:[^:]+:)[0-9]+>`)

func (bot *Chatbot)ClearMoreUnhandledDiscordery(msg string) string {
    msg = extraUnhandledMentions.ReplaceAllString(msg, `$1`)
    for rank, name := range bot.DiscordExtra.RankIDToName {
        msg = strings.Replace(msg, rank, name, -1)
    }
    return msg
}

func (bot *Chatbot)HandleMessageFromDiscord(s *discordgo.Session, m *discordgo.MessageCreate, fromDiscord *os.File) {
    if m.Author.ID == s.State.User.ID {
        // Ignore ourselves
        return
    }

    if m.Author.Bot {
        // Ignore other bots
        // yes, I am hardcoding /baleet Odebot.  Take that!
        return
    }

    relayConf         := GetRelayConf()
    targetChannel, ok := relayConf["from_discord_to_kol"][m.ChannelID]
    if !ok {
        if dm, _ := ComesFromDM(s, m); dm {
            bot.HandleDM(s, m)
        }
        return // Someone spoke in a channel we are not relaying
    }

    msgJson, _ := json.Marshal(m)
    fmt.Fprintf(fromDiscord, "%s: %s\n", time.Now().Format(time.RFC3339), msgJson)

    msg, err := m.ContentWithMoreMentionsReplaced(s)
    if err != nil {
        msg = m.ContentWithMentionsReplaced()
    }

    msg = bot.ClearMoreUnhandledDiscordery(msg)

    if m.Attachments != nil && len(m.Attachments) > 0 {
        for _, attachment := range m.Attachments {
            if len(msg) > 0 {
                msg += " "
            }
            msg += attachment.ProxyURL
        }
    }

    if msg == "" {
        // Empty message
        // We get here when someone sends a file/picture etc
        // with no message body.  Just skip it.
        return
    }

    if bot.GlobalStfu || bot.PartialStfu {
        return // respect the desire for silence
    }

    go bot.DiscordMessageTriggers(s, m)

    author    := sanitizeForKoL(bot.ResolveNickname(s, m))
    msgForKoL := sanitizeForKoL(msg)
    finalMsg  := author + ": " + msgForKoL

    kol       := bot.KoL

    if len(finalMsg) > 200 {
        // Hm..
        if len(finalMsg + author) < 300 {
            // Just split it
            kol.SendMessage(targetChannel, finalMsg[:150] + "...")
            kol.SendMessage(targetChannel, author + ": ..." + finalMsg[150:])
            return
        }
        // Too long!
        s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Brevity is the soul of wit, %s.  That message was too long, so it will not get relayed.", author))
        return
    }
    kol.SendMessage(targetChannel, finalMsg)
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
    // Connect to discord
    dg := NewDiscordConnection(discordConf.DiscordApiKey)

    // Conenct to KoL
    kol := kolgo.NewKoL(kolConf.Username, kolConf.Password, fromKoL)
    err := kol.LogIn()
    if err != nil {
        panic(err)
    }

    dbConf := DbConf()
    db, err := sql.Open(dbConf.DriverName, dbConf.DataSource)
    if err != nil {
        panic(err)
    }
    err = db.Ping()
    if err != nil {
        panic(err)
    }
    // Nice, sqlite works

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
    bot.FleshenSQLData()
    bot.FleshenAdministrators(defaultDiscordChannel, discordConf)

    return bot
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
    cleared, _ := bot.ClearJawBruiser()
    if ! cleared {
        fmt.Println("Started up jawbruised, and could not clear it!")
        bot.PartialStfu = true
    }
    // Clear the snowball effect.  No harm if we can't -- just a lousy
    // chat effect.
    go bot.ClearSnowball()

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

        toDiscord, err := HandleKoLPublicMessage(kol, message, discordConf.EffectTransform)
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

    toilbot := NewToilBot(toilConf.Username, toilConf.Password, bot.Db)
    toilbot.AddHandler(AcceptedApplication, func (app ClanApplication) {
        announcement := fmt.Sprintf(FCA_AnnounceKoLFmt, app.PlayerName, app.PlayerID)
        // Nice, we got a new clannie.  Make relay send them the welcome kmail:
        bot.KoL.SendKMail(app.PlayerName, FCA_WELCOME)
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

    fmt.Println("Bot is now running.  Press CTRL-C to exit.")
    sc := make(chan os.Signal, 1)
    signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
    <-sc
}


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

    "bytes"
    "os/exec"

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

func init() {
    rand.Seed(time.Now().UnixNano())

    if _, err := os.Stat(killFile); !os.IsNotExist(err) {
        panic(errors.New("Killfile exists, refusing to start"))
    }


    flag.StringVar(&dbConfJson,      "db_conf",      "", "Path to the the database config JSON file")
    flag.StringVar(&discordConfJson, "discord_conf", "", "Path to the the discord config JSON file")
    flag.StringVar(&kolConfJson,     "kol_conf",     "", "Path to the the KoL config JSON file")
    flag.StringVar(&relayConfJson,   "relay_conf",   "", "Path to the the relay targets JSON file")
    flag.Parse()

    FleshenSQLData()

    _ = GetKoLConf()
    _ = GetDiscordConf()
    _ = GetRelayConf()

    tryLynx = DetectLynx()
}

var dbConfJson, discordConfJson, kolConfJson, relayConfJson string

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
    DiscordApiKey string `json:"discord_api_key"`
    AdminRole     string `json:"admin_role"`
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


var gameNameOverride sync.Map
var alreadyGrumbledFast sync.Map

func FleshenSQLData() {
    dbConf := DbConf()
    db, err := sql.Open(dbConf.DriverName, dbConf.DataSource)
    if err != nil {
        panic(err)
    }
    defer db.Close()
    err = db.Ping()
    if err != nil {
        panic(err)
    }
    // Nice, sqlite works

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
            gameNameOverride.Store(discordId, nickname)
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
            alreadyGrumbledFast.Store(discordId, true)
        }
        err = rows.Err()
        if err != nil {
            panic(err)
        }
    }()

    wg.Wait()
}

var globalStfu bool = false
func RelayToDiscord(dg *discordgo.Session, destChannel string, toDiscord string) {
    if globalStfu {
        return
    }
    dg.ChannelMessageSend(destChannel, toDiscord)
}

func NewDiscordConnection(botAPIKey string) *discordgo.Session {
    dg, err := discordgo.New("Bot " + botAPIKey)

    err = dg.Open()
    if err != nil {
        panic(err)
    }

    return dg
}

func ResolveNickname(s *discordgo.Session, m *discordgo.MessageCreate) string {
    id := m.Author.ID

    result, ok := gameNameOverride.Load(id)
    if ok {
        return result.(string)
    }

    go GrumbleIfNicknameAndUsernameDiffer(s, m)

    return m.Author.Username
}

func InsertNicknameGrumble(discordId string) {
    // Put it in the in-memory cache first:
    alreadyGrumbledFast.Store(discordId, true)

    sqliteInsert.Lock()
    defer sqliteInsert.Unlock()
    dbConf := DbConf()
    db, err := sql.Open(dbConf.DriverName, dbConf.DataSource)
    if err != nil {
        fmt.Println("Had an error opening kol_relay.db")
        return
    }
    defer db.Close()

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

func GrumbleIfNicknameAndUsernameDiffer(s *discordgo.Session, m *discordgo.MessageCreate) {
    _, ok := alreadyGrumbledFast.Load(m.Author.ID)
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
            alreadyGrumbledFast.Store(m.Author.ID, true)
            return // No nickname
        }
        nick := member.Nick
        if strings.EqualFold(nick, m.Author.Username) {
            alreadyGrumbledFast.Store(m.Author.ID, true)
            return
        }

        userChannel, err := s.UserChannelCreate(m.Author.ID)
        if err != nil {
            fmt.Println("Could not ping someone about their username, error: ", err)
            return
        }

        // Okay, so their nickname differs from their username.  Have we poked them before?
        go InsertNicknameGrumble(m.Author.ID)
        msg := fmt.Sprintf("To prevent abuse, the relay has to use your discord username (%s) when showing messages in KoL, not your nickname (%s).\n\nTo make it use your in-game name, send it a private message in-game with this: `/msg RelayBot Verify` and follow the instructions.", m.Author.Username, nick)
        s.ChannelMessageSend(userChannel.ID, msg)

        return
    }
}

func EmojiNoMore(s string) string {
    for i, w := 0, 0; i < len(s); i += w {
        c, width := utf8.DecodeRuneInString(s[i:])
        w = width
        if c > 0xFF && unicode.IsSymbol(c) {
            name := runenames.Name(c)
            s = s[:i] + "[" + name + "]" + s[i+w:]
            w = len(name) + 2
        }
    }
    return s
}

// This should be [\p{Latin1}\p{ASCII}], but no such thing in golang
var nonLatin1Re *regexp.Regexp = regexp.MustCompile(`[^\x00-\xff]`)
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

func ComesFromDM(s *discordgo.Session, m *discordgo.MessageCreate) (bool, error) {
    channel, err := s.State.Channel(m.ChannelID)
    if err != nil {
        if channel, err = s.Channel(m.ChannelID); err != nil {
            return false, err
        }
    }

    return channel.Type == discordgo.ChannelTypeDM, nil
}

type dmHandlers struct {
    re *regexp.Regexp
    cb func(*discordgo.Session, *discordgo.MessageCreate, []string, kolgo.KoLRelay)
}

func IsAdminRole(s *discordgo.Session, m *discordgo.MessageCreate) bool {
    _, ok := administrators[m.Author.ID]
    if ok {
        return true
    }

    return false
}

func SenderCanRunCommands(s *discordgo.Session, m *discordgo.MessageCreate) bool {
    if IsAdminRole(s, m) {
        return true
    }

    if m.Author.Username == "hugmeir" {
        return true
    }

    return false
}

func SenderIsModerator(s *discordgo.Session, m *discordgo.MessageCreate) bool {
    if SenderCanRunCommands(s, m) {
        return true
    }

    _, ok := moderators[m.Author.ID]
    if ok {
        return true
    }

    return false
}

func HandleCommandForGame(s *discordgo.Session, m *discordgo.MessageCreate, matches []string, kol kolgo.KoLRelay) {
    if !SenderCanRunCommands(s, m) {
        return
    }

    output, err := kol.SubmitChat(matches[1], matches[2])
    if err != nil {
        s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Command run FAILED, error: ```css\n%s\n```", err))
        return
    }

    s.ChannelMessageSend(m.ChannelID, "Command run, output: ```css\n" + string(output) + "\n```")
}

var tryLynx bool = false
func DetectLynx() bool {
    str, err := exec.LookPath("lynx")
    if err != nil {
        return false
    }

    if str == "" {
        return false
    }

    cmd := exec.Command("lynx", "-help")
    var out bytes.Buffer
    cmd.Stdout = &out
    err  = cmd.Run()
    if err != nil {
        return false
    }

    if err != nil {
        return false
    }

    if !strings.Contains(out.String(), `-dump`) {
        return false
    }

    // What a gauntlet!
    return true
}

// Try formatting the game output with lynx
func FormatGameOutput(o []byte) string {
    if !tryLynx {
        return string(o)
    }

    cmd := exec.Command("lynx", "--dump", "--stdin")
    cmd.Stdin = bytes.NewReader(o)
    var out bytes.Buffer
    cmd.Stdout = &out
    err := cmd.Run()
    if err != nil {
        return string(o)
    }

    return out.String()
}

// TODO: Move this into kolgo and just steal the item list from mafia
type ItemType int
const (
    Spleen ItemType = iota
    Usable
)
type Item struct {
    ID   string
    Name string
    Type ItemType
}
var itemNameToID map[string]*Item = map[string]*Item{
    "sleaze wad": &Item{
        ID:   "1455",
        Name: "sleaze wad",
        Type: Spleen,
    },
    "mojo filter": &Item{
        ID: "2614",
        Name: "mojo filter",
        Type: Usable,
    },
}
var validItemID *regexp.Regexp = regexp.MustCompile(`\A[0-9]+\z`)
func ValidItemID(itemID string) bool {
    return validItemID.MatchString(itemID)
}
func HandleUseCommand(s *discordgo.Session, m *discordgo.MessageCreate, matches []string, kol kolgo.KoLRelay) {
    if !SenderCanRunCommands(s, m) {
        return
    }

    itemID         := matches[2]
    quantityStr    := matches[1]
    actualItem, ok := itemNameToID[strings.ToLower(itemID)]
    if ok {
        itemID = actualItem.ID
    }

    if !ValidItemID(itemID) {
        s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Sorry, don't know what to do with that item"))
        return
    }

    quantity := 1
    q, err := strconv.Atoi(quantityStr)
    if err == nil && q > 0 {
        quantity = q
    }

    output, err  := kol.InvUse(itemID, quantity)
    if err != nil {
        s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Command run FAILED, error: ```css\n%s\n```", err))
        return
    }

    formattedOutput := FormatGameOutput(output)

    // TODO: escape `s!
    s.ChannelMessageSend(m.ChannelID, "Command run, output: ```css\n" + formattedOutput + "\n```")
}
func HandleChewCommand(s *discordgo.Session, m *discordgo.MessageCreate, matches []string, kol kolgo.KoLRelay) {
    if !SenderCanRunCommands(s, m) {
        return
    }

    itemID         := matches[1]
    actualItem, ok := itemNameToID[strings.ToLower(itemID)]
    if ok {
        itemID = actualItem.ID
    }

    if !ValidItemID(itemID) {
        s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Sorry, don't know what to do with that item"))
        return
    }

    output, err  := kol.InvSpleen(itemID)
    if err != nil {
        s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Command run FAILED, error: ```css\n%s\n```", err))
        return
    }

    formattedOutput := FormatGameOutput(output)

    // TODO: escape `s!
    s.ChannelMessageSend(m.ChannelID, "Command run, output: ```css\n" + formattedOutput + "\n```")
}

const killFile = "/tmp/kol-relay-KILL"

var allDMHandlers = []dmHandlers {
    dmHandlers {
        regexp.MustCompile(`(?i)^\s*verify(?:\s* me)?: ([0-9]{10,})`),
        HandleVerification,
    },
    dmHandlers {
        // !cmd /...
        // Will execute the /... in-game as the relay:
        //  !cmd /msg Hugmeir oh hey boss
        // That will send me a message.  Don't spam me >.>
        regexp.MustCompile(`(?i)!(?:cmd|powerword) (/[^\s]+)(\s*.*)`),
        HandleCommandForGame,
    },
    dmHandlers {
        // !cmd alias ...
        //
        // Does nothing.  !cmd alias only works on the main channel, to prevent
        // stealthy names.
        regexp.MustCompile(`(?i)!(?:cmd|powerword) alias`),
        func (s *discordgo.Session, m *discordgo.MessageCreate, matches []string, kol kolgo.KoLRelay) {
            s.ChannelMessageSend(m.ChannelID, "To prevent shenanigans, the alias command only works on the main channel, NOT through direcct message")
        },
    },
    dmHandlers {
        // !cmd Kill
        //
        // This is the killswitch for the relay.
        //
        // It stops the relay and prevents it from coming back up until manually checked by someone
        // with access to the box it is running on.
        //
        // For use in emergencies!
        regexp.MustCompile(`(?i)\A!(?:cmd|powerword) Kill\z`),
        func(s *discordgo.Session, m *discordgo.MessageCreate, matches []string, kol kolgo.KoLRelay) {
            if !SenderCanRunCommands(s, m) {
                s.ChannelMessageSend(m.ChannelID, "That would've totes done something if you had the rights to do the thing.")
                return
            }

            _, err := os.OpenFile(killFile, os.O_RDONLY|os.O_CREATE, 0666)
            if err != nil {
                s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Going down, but could not prevent respawning, so the bot will return in 5 minutes.  Reason given: %s", err))
            } else {
                s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Going down, will NOT come back until the killfile is manually removed"))
            }
            panic(errors.New(fmt.Sprintf("*Killed* by %s", m.Author.Username)))
        },
    },
    dmHandlers {
        // !cmd Crash
        //
        // Will crash the relay.  It will come back in ~5 minutes or so.
        // Basically a 'did you turn it off and on again' command.
        regexp.MustCompile(`(?i)\A!(?:cmd|powerword) Crash\z`),
        func(s *discordgo.Session, m *discordgo.MessageCreate, matches []string, kol kolgo.KoLRelay) {
            if SenderCanRunCommands(s, m) {
                s.ChannelMessageSend(m.ChannelID, "Crashing, bot should return in ~5m")
                panic(errors.New(fmt.Sprintf("Asked to crash by %s", m.Author.Username)))
            } else {
                s.ChannelMessageSend(m.ChannelID, "That would've totes done something if you had the rights to do the thing.")
            }
        },
    },
    dmHandlers {
        // !cmd stfu
        // !cmd stop
        //
        // Will make it stop relaying messages.
        regexp.MustCompile(`(?i)\A!(?:cmd|powerword) (?:Relay(?:Bot),?\s+)?(?:stfu|stop)`),
        func(s *discordgo.Session, m *discordgo.MessageCreate, matches []string, kol kolgo.KoLRelay) {
            if SenderCanRunCommands(s, m) {
                globalStfu = true
                s.ChannelMessageSend(m.ChannelID, "Floodgates are CLOSED.  No messages will be relayed")
            } else {
                s.ChannelMessageSend(m.ChannelID, "That would've totes done something if you had the rights to do the thing.")
            }
        },
    },
    dmHandlers {
        // !cmd use AMOUNT ITEMID/[ITEM NAME]
        //
        // Will make it use the AMOUNT of ITEM
        regexp.MustCompile(`(?i)\A!(?:cmd|powerword)\s+use\s+(\d)\s+(.+)`),
        HandleUseCommand,
    },
    dmHandlers {
        // !cmd chew ITEMID
        //
        // Will make it chew (spleen-use) the item
        regexp.MustCompile(`(?i)\A!(?:cmd|powerword)\s+chew\s+(.+)`),
        HandleChewCommand,
    },
    dmHandlers {
        // !cmd spam on
        // !cmd start
        //
        // Will make it start relaying messages if previously stfu'd
        regexp.MustCompile(`(?i)\A!(?:cmd|powerword) (?:Relay(?:Bot),?\s+)?(?:spam on|start)`),
        func(s *discordgo.Session, m *discordgo.MessageCreate, matches []string, kol kolgo.KoLRelay) {
            if SenderCanRunCommands(s, m) {
                globalStfu  = false
                partialStfu = false
                s.ChannelMessageSend(m.ChannelID, "Floodgates are open")
            } else {
                s.ChannelMessageSend(m.ChannelID, "That would've totes done something if you had the rights to do the thing.")
            }
        },
    },
}

func HandleDM(s *discordgo.Session, m *discordgo.MessageCreate, kol kolgo.KoLRelay) {
    for _, handler := range allDMHandlers {
        re      := handler.re
        matches := re.FindStringSubmatch(m.Content)
        if len(matches) > 0 {
            go handler.cb(s, m, matches, kol)
            // One match per DM
            return
        }
    }

    s.ChannelMessageSend(m.ChannelID, "<some funny message about not understanding what you mean>");
}

var sqliteInsert sync.Mutex
func InsertNewNickname(discordId string, nick string) {
    sqliteInsert.Lock()
    defer sqliteInsert.Unlock()
    dbConf := DbConf()
    db, err := sql.Open(dbConf.DriverName, dbConf.DataSource)
    if err != nil {
        fmt.Println("Had an error opening kol_relay.db")
        return
    }
    defer db.Close()

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

func HandleAliasing(s *discordgo.Session, m *discordgo.MessageCreate, matches []string, kol kolgo.KoLRelay) {
    discordName := matches[1]
    kolName     := matches[2]

    if !SenderIsModerator(s, m) {
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

    go InsertNewNickname(discordID, kolName)
    // Put in our in-memory hash:
    gameNameOverride.Store(discordID, kolName)
    // Let 'em know:
    s.ChannelMessageSend(m.ChannelID, "Alias registered.  Reflect on your mistakes.")
}

var verificationsPending sync.Map
func HandleVerification(s *discordgo.Session, m *discordgo.MessageCreate, matches []string, kol kolgo.KoLRelay) {
    verificationCode := matches[1]
    result, ok := verificationsPending.Load("Code:" + verificationCode)
    if ok {
        // Insert in the db:
        go InsertNewNickname(m.Author.ID, result.(string))
        // Put in our in-memory hash:
        gameNameOverride.Store(m.Author.ID, result.(string))
        // Let 'em know:
        s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("That's you alright!  I'll call you %s from now on", result.(string)))
    } else {
        // Hmm...
        s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Incorrect verification code: '%s'", verificationCode))
    }
}

/*
{"msgs":[{"msg":"A new trivial update has been posted: You can now walk away from the intro choice in the Neverending Party if you want, like if you accidentally show up wearing the wrong shirt or something.","type":"system","mid":"1468408333","who":{"name":"System Message","id":"-1","color":""},"format":"2","channelcolor":"green","time":"1537455943"}],"last":"1468408333","delay":3000}
*/
func HandleKoLSystemMessage(kol kolgo.KoLRelay, message kolgo.ChatMessage) (string, error) {
    msg := EscapeDiscordMetaCharacters(message.Msg)
    toDiscord := fmt.Sprintf("```css\n%s: %s\n```", message.Who.Name, msg)
    return toDiscord, nil
}

var firstVerifyRe *regexp.Regexp = regexp.MustCompile(`(?i)^\s*verify(?:\s* me)?!?`)
func HandleKoLDM(kol kolgo.KoLRelay, message kolgo.ChatMessage) (string, error) {
    if !firstVerifyRe.MatchString(message.Msg) {
        return "", nil
    }

    senderId := kol.SenderIdFromMessage(message)

    _, ok := verificationsPending.Load("User:" + message.Who.Name);
    if ok {
        kol.SendMessage("/msg " + senderId, "Already sent you a code, you must wait 5 minutes to generate a new one")
        return "", nil
    }

    verificationCode := fmt.Sprintf("%15d", rand.Uint64())
    verificationsPending.Store("Code:" + verificationCode, message.Who.Name)
    verificationsPending.Store("User:" + message.Who.Name, verificationCode)

    kol.SendMessage("/msg " + senderId, "In Discord, send me a private message saying \"Verify me: " + verificationCode + "\", without the quotes.  This will expire in 5 minutes")

    go func() {
        time.Sleep(5 * time.Minute)
        verificationsPending.Delete("Code:" + verificationCode)
        verificationsPending.Delete("User:" + message.Who.Name)
    }()

    return "", nil
}

const (
    bruisedJaw = 697
    snowBall   = 718
)

func ClearJawBruiser(kol kolgo.KoLRelay) (bool, error) {
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

func ClearSnowball(kol kolgo.KoLRelay) (bool, error) {
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

var partialStfu bool = false
var jawBruiser *regexp.Regexp = regexp.MustCompile(`(?i)<a href='showplayer\.php\?who=([0-9]+)' [^>]+>([^<]+)<\/a> has hit you in the jaw with a piece of candy`)
/*
{"msgs":[{"type":"event","msg":"That rotten jerk <a href='showplayer.php?who=3061055' target=mainpane class=nounder style='color: green'>Hugmeir<\/a> plastered you in the face with a snowball! Grr! Also, Brr!<!--refresh-->","link":false,"time":"1537390984"}],"last":"1468370925","delay":3000}
*/
var snowBalled *regexp.Regexp = regexp.MustCompile(`(?i)<a href='showplayer\.php\?who=([0-9]+)' [^>]+>([^<]+)<\/a> plastered you in the face with a snowball`)
func HandleKoLEvent(kol kolgo.KoLRelay, message kolgo.ChatMessage) (string, error) {
    matches := jawBruiser.FindStringSubmatch(message.Msg)
    if len(matches) > 0 {
        fmt.Printf("Jawbruised by %s (%s), raw message: %s", matches[1], matches[2], message.Msg)
        senderId := matches[1]
        kol.SendMessage("/msg " + senderId, "C'mon, don't be a dick.")

        cleared, _ := ClearJawBruiser(kol)
        toDiscord := fmt.Sprintf("%s (#%s) jawbruised the bot.", matches[2], matches[1])
        if ! cleared {
            partialStfu = true
            toDiscord   = toDiscord + " And it could not be uneffected, so the bot will stop relaying messages to KoL."
        }
        return toDiscord, nil
    }

    matches = snowBalled.FindStringSubmatch(message.Msg)
    if len(matches) > 0 {
        fmt.Printf("Hit by a snowball from %s (%s), raw message: %s", matches[1], matches[2], message.Msg)
        senderId := matches[1]
        kol.SendMessage("/msg " + senderId, "How about you don't?  That'll just be irritating for people reading chat.")

        cleared, _ := ClearSnowball(kol)
        toDiscord := fmt.Sprintf("%s (#%s) threw a snowball at the bot.", matches[2], matches[1])
        if ! cleared {
            toDiscord = toDiscord + " And it could not be uneffected, so the relayed messages will get effects."
        }
        return toDiscord, nil
    }

    return "", nil
}

type triggerTuple struct {
    re *regexp.Regexp
    cb func(*discordgo.Session, *discordgo.MessageCreate, []string, kolgo.KoLRelay)
}

var bullshitTriggers = []triggerTuple {
    triggerTuple {
        regexp.MustCompile(`(?i)/whois\s+hugmeir`),
        func(s *discordgo.Session, m *discordgo.MessageCreate, matches []string, kol kolgo.KoLRelay) {
            if m.Author.Username == "hugmeir" {
                return
            }
            s.ChannelMessageSend(m.ChannelID, "Ssh! Don't summon the accursed creator!")
        },
    },
    triggerTuple {
        regexp.MustCompile(`(?i)^\s*Relay(?:Bot)?,?\s+tell\s+me\s+about\s+(.+)`),
        func(s *discordgo.Session, m *discordgo.MessageCreate, matches []string, kol kolgo.KoLRelay) {
            about := matches[1]
            if strings.Contains(about, "IO ERROR") {
                s.ChannelMessageSend(m.ChannelID, "Still doing that, are you?  You'll want to talk to one of _those_ other bots")
            } else if strings.Contains(about, "verification") {
                msg := "The relay uses your discord username.  To make it use your in-game name, `/msg RelayBot Verify` in-game and follow the instructions"
                s.ChannelMessageSend(m.ChannelID, msg)
                kol.SendMessage(`/clan`, msg)
            }
        },
    },
    triggerTuple {
        regexp.MustCompile(`(?i)\A!(?:cmd|powerword) (?:alias|verif(?:y|ication)) (.+) (?:(?:as|to) )?(.+)\z`),
        HandleAliasing,
    },

    // ALWAYS LAST
    triggerTuple {
        regexp.MustCompile(`(?i)\A!(?:cmd|powerword)\s+([a-zA-Z0-9]*)`),
        func (s *discordgo.Session, m *discordgo.MessageCreate, matches []string, kol kolgo.KoLRelay) {
            cmd := matches[1]
            if cmd == "" {
                cmd = "unknown"
            }
            s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Speak loudly dear, I don't know what this '%s' command means", cmd))
        },
    },
}

func RandomBullshit(s *discordgo.Session, m *discordgo.MessageCreate, kol kolgo.KoLRelay ) {
    content := m.Content

    for _, trigger := range bullshitTriggers {
        re      := trigger.re
        matches := re.FindStringSubmatch(content)
        if len(matches) == 0 {
            continue
        }
        trigger.cb(s, m, matches, kol)
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
var extraUnhandledMentions *regexp.Regexp = regexp.MustCompile(`(?i)<(:[^:]+:)[0-9]+>`)

func ClearMoreUnhandledDiscordery(msg string) string {
    msg = extraUnhandledMentions.ReplaceAllString(msg, `$1`)
    for rank, name := range rankIDToName {
        msg = strings.Replace(msg, rank, name, -1)
    }
    return msg
}

func HandleMessageFromDiscord(s *discordgo.Session, m *discordgo.MessageCreate, fromDiscord *os.File, kol kolgo.KoLRelay) {
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
            HandleDM(s, m, kol)
        }
        return // Someone spoke in a channel we are not relaying
    }

    msgJson, _ := json.Marshal(m)
    fmt.Fprintf(fromDiscord, "%s: %s\n", time.Now().Format(time.RFC3339), msgJson)

    msg, err := m.ContentWithMoreMentionsReplaced(s)
    if err != nil {
        msg = m.ContentWithMentionsReplaced()
    }

    msg = ClearMoreUnhandledDiscordery(msg)

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

    if globalStfu || partialStfu {
        return // respect the desire for silence
    }

    go RandomBullshit(s, m, kol)

    author    := sanitizeForKoL(ResolveNickname(s, m))
    msgForKoL := sanitizeForKoL(msg)
    finalMsg  := author + ": " + msgForKoL

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

var administrators map[string]bool   = make(map[string]bool, 20)
var moderators     map[string]bool   = make(map[string]bool, 20)
var rankIDToName   map[string]string = make(map[string]string, 20)
const discordModeratorRole = `Dabomox`
func FleshenAdministrators(s *discordgo.Session, defaultDiscordChannel string, discordAdminRole string) {
    c, err := s.Channel(defaultDiscordChannel)
    if err != nil {
        return
    }

    g, err := s.Guild(c.GuildID)
    guildRoles := g.Roles
    if err != nil {
        return
    }

    adminRole := ""
    moderatorRole := ""
    for _, r := range guildRoles {
        rankIDToName["<@&" + r.ID + ">"] = r.Name

        if r.Name == discordAdminRole {
            adminRole = r.ID
        }
        if r.Name == discordModeratorRole {
            moderatorRole = r.ID
        }
    }

    if adminRole == "" {
        return
    }

    for _, member := range g.Members {
        for _, roleName := range member.Roles {
            if roleName == adminRole {
                administrators[member.User.ID] = true
                moderators[member.User.ID] = true
            } else if roleName == moderatorRole {
                moderators[member.User.ID] = true
            }
        }
    }
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

    // Connect to discord
    dg := NewDiscordConnection(discordConf.DiscordApiKey)
    // Cleanly close down the Discord session.
    defer dg.Close()

    // TODO: probably broken due to accessing a map concurrently
    go FleshenAdministrators(dg, defaultDiscordChannel, discordConf.AdminRole)

    // Conenct to KoL
    kol := kolgo.NewKoL(kolConf.Username, fromKoL)
    err  = kol.LogIn(kolConf.Password)
    if err != nil {
        panic(err)
    }
    // Cleanly disconnect from KoL
    defer kol.LogOut()

    // This handler is called when the bot sees a message on discord
    dg.AddHandler(func (s *discordgo.Session, m *discordgo.MessageCreate) {
        HandleMessageFromDiscord(s, m, fromDiscord, kol)
    })

    // Start the chat poller.
    go kol.StartChatPoll(kolConf.Password)
    go kol.StartMessagePoll(kolConf.Password)

    // Clear the Bruised Jaw effect.  If we fail, do not relay messages
    // from discord into kol
    cleared, _ := ClearJawBruiser(kol)
    if ! cleared {
        fmt.Println("Started up jawbruised, and could not clear it!")
        partialStfu = true
    }
    // Clear the snowball effect.  No harm if we can't -- just a lousy
    // chat effect.
    ClearSnowball(kol)

    // Try sending the initial message to confirm that everything is working
    // NOTE: We use SubmitChat, not the "nicer" interface SendMessage here,
    // because want to die if this fails to send.
    _, err = kol.SubmitChat("/msg hugmeir", "oh hai creator")
    if err != nil {
        fmt.Println("Cannot send initial message, something has gone wrong: %v", err)
        panic(err)
    }

    // This handler is called when we see a "public" message in KoL chat -- a public
    // message is basically anything that is not a private message (/msg), and event
    // (like getting hit with a jawbruiser) or a system message (trivial announcements)
    kol.AddHandler(kolgo.Public, func (kol kolgo.KoLRelay, message kolgo.ChatMessage) {
        targetDiscordChannel, ok := relayConf["from_kol_to_discord"][message.Channel]
        if !ok {
            return
        }

        toDiscord, err := HandleKoLPublicMessage(kol, message)
        if err != nil {
            // TODO
            return
        }
        if toDiscord == "" {
            return
        }
        RelayToDiscord(dg, targetDiscordChannel, toDiscord)
    })

    // Called when we see a system message in KoL.  Currently untested because, well,
    // those are rare >.>
    kol.AddHandler(kolgo.System, func (kol kolgo.KoLRelay, message kolgo.ChatMessage) {
        toDiscord, err := HandleKoLSystemMessage(kol, message)
        if err != nil {
            // TODO
            return
        }
        if toDiscord == "" {
            return
        }
        RelayToDiscord(dg, defaultDiscordChannel, toDiscord)
    })

    // Called when we get a private message in KoL
    kol.AddHandler(kolgo.Private, func (kol kolgo.KoLRelay, message kolgo.ChatMessage) {
        toDiscord, err := HandleKoLDM(kol, message)
        if err != nil {
            // TODO
            return
        }
        if toDiscord == "" {
            return
        }
        RelayToDiscord(dg, defaultDiscordChannel, toDiscord)
    })

    // Called when we see an 'event', like getting jawbruised or snowballed
    kol.AddHandler(kolgo.Event, func (kol kolgo.KoLRelay, message kolgo.ChatMessage) {
        toDiscord, err := HandleKoLEvent(kol, message)
        if err != nil {
            // TODO
            return
        }
        if toDiscord == "" {
            return
        }
        RelayToDiscord(dg, defaultDiscordChannel, toDiscord)
    })

    fmt.Println("Bot is now running.  Press CTRL-C to exit.")
    sc := make(chan os.Signal, 1)
    signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
    <-sc
}


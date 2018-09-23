package main
import (
    "os"
    "os/signal"
    "sync"
    "strconv"
    "regexp"
    "fmt"
    "time"
    "flag"
    "syscall"
    "golang.org/x/net/html"

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

    flag.StringVar(&dbConfJson,      "db_conf",      "", "Path to the the database config JSON file")
    flag.StringVar(&discordConfJson, "discord_conf", "", "Path to the the discord config JSON file")
    flag.StringVar(&kolConfJson,     "kol_conf",     "", "Path to the the KoL config JSON file")
    flag.StringVar(&relayConfJson,   "relay_conf",   "", "Path to the the relay targets JSON file")
    flag.Parse()

    LoadNameOverrides()

    _ = GetKoLConf()
    _ = GetDiscordConf()
    _ = GetRelayConf()
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


func LoadNameOverrides() {
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
    rows, err := db.Query("SELECT discord_id, nickname FROM discord_name_override")
    if err != nil {
        panic(err)
    }
    defer rows.Close()
    for rows.Next() {
        var discordId string
        var nickname   string
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
}

// Sigh, discord, why...
// We wrap all messages in ``s to prevent abuse.
// So the only metacharacter we need to quote is '`'.  Even '\' is not a metacharacter.. unless
// it precedes a '`'.
// Due to purist crap reasons, no positive lookbehind (?<=\\) in the default regex
// engine, and no recursive regexes either, so just try the long way:
var metaRegexp *regexp.Regexp = regexp.MustCompile(
    // If the grave is already quoted (preceded by a backslash), no need to grab it
    `(?:`                       +
        `^`                     + // begining of string followed by a `
        `|[^\\]`                + // or a grave not preceded by a backslash
        `|(?:^|[^\\])(?:\\\\)+` + // Or a grave preceded by escaped backslashes
    `)`                         +
    `([\x60])`,                   // Capture the grave, in case we need to extend this eventually...
)
func EscapeDiscordMetaCharacters(s string) string {
    return metaRegexp.ReplaceAllString(s, `\$1`)
}

var globalStfu bool = false
func RelayToDiscord(dg *discordgo.Session, destChannel string, toDiscord string) {
    if globalStfu {
        return
    }
    dg.ChannelMessageSend(destChannel, toDiscord)
}

func HandleKoLPublicMessage(kol kolgo.KoLRelay, message kolgo.ChatMessage) (string, error) {
    rawMessage     := message.Msg;
    cleanedMessage := html.UnescapeString(rawMessage)
    if strings.HasPrefix(cleanedMessage, "<") {
        // golden text, chat effects, etc.
        tokens := html.NewTokenizer(strings.NewReader(cleanedMessage))
        cleanedMessage = ""
        loop:
        for {
            tt := tokens.Next()
            switch tt {
            case html.ErrorToken:
                break loop
            case html.TextToken:
                cleanedMessage = cleanedMessage + string(tokens.Text())
            }
            // TODO: could grab colors & apply them in markdown
        }
    }

    cleanedMessage = EscapeDiscordMetaCharacters(cleanedMessage)

    optionalChannel := ""
    if message.Channel != "clan" {
        optionalChannel = fmt.Sprintf("[%s] ", message.Channel)
    }

    return fmt.Sprintf("%s**%s**: `%s`", optionalChannel, message.Who.Name, cleanedMessage), nil
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

    // TODO: worth checking if nickname != username and spamming?

    return m.Author.Username
}

/*
    c, err := s.Channel(m.ChannelID)
    if err != nil {
        return m.Author.Username
    }

    g, err := s.Guild(c.GuildID)
    if err != nil {
        return m.Author.Username
    }

    for _, member := range g.Members {
        if m.Author.ID != member.User.ID {
            continue;
        }
        if member.Nick != "" {
            nick := member.Nick
            if nick == m.Author.Username {
                return nick
            }
            // Only return nickna
            return member.Nick
        }
        break;
    }

    return m.Author.Username // fallback
}
*/

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

func SenderCanRunCommands(s *discordgo.Session, m *discordgo.MessageCreate) bool {
    if m.Author.Username == "hugmeir" {
        return true
    }

    return false
}

func HandleCommandForGame(s *discordgo.Session, m *discordgo.MessageCreate, matches []string, kol kolgo.KoLRelay) {
    now := time.Now()
    if !SenderCanRunCommands(s, m) {
        return
    }

    kol.SendMessage(&kolgo.MessageToKoL{ matches[0], matches[1], now, kolgo.Command })
}

var allDMHandlers = []dmHandlers {
    dmHandlers {
        regexp.MustCompile(`(?i)^\s*verify(?:\s* me)?: ([0-9]{10,})`),
        HandleVerification,
    },
    dmHandlers {
        regexp.MustCompile(`(?i)!c(?:md|ommand) (/[^\s]+)(\s*.*)`),
        HandleCommandForGame,
    },
}
func HandleDM(s *discordgo.Session, m *discordgo.MessageCreate, kol kolgo.KoLRelay) {
    for _, handler := range allDMHandlers {
        re      := handler.re
        matches := re.FindStringSubmatch(m.Content)
        if len(matches) > 0 {
            handler.cb(s, m, matches, kol)
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
    if err != nil {
        affected, _ := res.RowsAffected()
        if affected > 0 {
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
        fmt.Println("Entirely saved to save details for ", discordId, nick)
    }

    affected, _ := res.RowsAffected()
    if affected < 1 {
        fmt.Println("Entirely saved to save details for ", discordId, nick)
    }
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
        kol.SubmitChat("/msg " + senderId, "Already sent you a code, you must wait 5 minutes to generate a new one")
        return "", nil
    }

    verificationCode := fmt.Sprintf("%15d", rand.Uint64())
    verificationsPending.Store("Code:" + verificationCode, message.Who.Name)
    verificationsPending.Store("User:" + message.Who.Name, verificationCode)

    kol.SubmitChat("/msg " + senderId, "In Discord, send me a private message saying \"Verify me: " + verificationCode + "\", without the quotes.  This will expire in 5 minutes")
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
        kol.SubmitChat("/msg " + senderId, "C'mon, don't be a dick.")

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
        kol.SubmitChat("/msg " + senderId, "How about you don't?  That'll just be irritating for people reading chat.")

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
    cb func(*discordgo.Session, *discordgo.MessageCreate, []string)
}

var bullshitTriggers = []triggerTuple {
    triggerTuple {
        regexp.MustCompile(`(?i)/whois\s+hugmeir`),
        func(s *discordgo.Session, m *discordgo.MessageCreate, matches []string) {
            if m.Author.Username == "hugmeir" {
                return
            }
            s.ChannelMessageSend(m.ChannelID, "Ssh! Don't summon the accursed creator!")
        },
    },
    triggerTuple {
        regexp.MustCompile(`(?i)^\s*Relay(?:Bot)?,?\s+tell\s+me\s+about\s+(.+)`),
        func(s *discordgo.Session, m *discordgo.MessageCreate, matches []string) {
            about := matches[1]
            if strings.Contains(about, "IO ERROR") {
                s.ChannelMessageSend(m.ChannelID, "Still doing that, are you?  You'll want to talk to one of _those_ other bots")
            } else if strings.Contains(about, "verification") {
                s.ChannelMessageSend(m.ChannelID, "The relay uses your discord username.  To make it use your in-game name, `/msg RelayBot Verify` in-game and follow the instructions")
            }
        },
    },
}

func RandomBullshit(s *discordgo.Session, m *discordgo.MessageCreate ) {
    content := m.Content

    for _, trigger := range bullshitTriggers {
        re      := trigger.re
        matches := re.FindStringSubmatch(content)
        if len(matches) == 0 {
            continue
        }
        trigger.cb(s, m, matches)
        return
    }
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

    if m.Content == "" {
        // Empty message
        // We get here when someone sends a file/picture etc
        // with no message body.  Just skip it.
        return
    }

    if m.Content == "RelayBot, stfu" {
        // We have been asked to quit it, so do!
        globalStfu = true
        return
    }

    if m.Content == "RelayBot, spam on" {
        globalStfu = false
        partialStfu = false
        return
    }

    if globalStfu || partialStfu {
        return // respect the desire for silence
    }

    go RandomBullshit(s, m)

    author    := sanitizeForKoL(ResolveNickname(s, m))
    msgForKoL := sanitizeForKoL(m.Content)
    finalMsg  := author + ": " + msgForKoL
    now       := time.Now()

    if len(finalMsg) > 200 {
        // Hm..
        if len(finalMsg + author) < 300 {
            // Just split it
            kol.SendMessage(&kolgo.MessageToKoL{ targetChannel, finalMsg[:150] + "...",            now, kolgo.Message })
            kol.SendMessage(&kolgo.MessageToKoL{ targetChannel, author + ": ..." + finalMsg[150:], now, kolgo.Message })
            return
        }
        // Too long!
        s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Brevity is the soul of wit, %s.  That message was too long, so it will not get relayed.", author))
        return
    }
    kol.SendMessage(&kolgo.MessageToKoL{ targetChannel, finalMsg, now, kolgo.Message })
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


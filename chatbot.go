package main
import (
    "os"
    "os/signal"
    "sync"
    "strconv"
    "regexp"
    "fmt"
    "time"
    "syscall"
    "net/url"
    "golang.org/x/net/html"
    "golang.org/x/text/encoding/charmap"
    "net/http"
    "net/http/cookiejar"
    "io/ioutil"
    "bytes"
    "strings"
    "errors"
    "math/rand"
    "encoding/json"
    "compress/gzip"
    "database/sql"
    _ "github.com/mattn/go-sqlite3"
    "github.com/bwmarrin/discordgo"
)

const baseUrl          = "https://www.kingdomofloathing.com/"
const (
    loginUrl         = baseUrl + "login.php"
    logoutUrl        = baseUrl + "logout.php"
    newMessageUrl    = baseUrl + "newchatmessages.php"
    submitMessageUrl = baseUrl + "submitnewchat.php"
    lChatUrl         = baseUrl + "lchat.php"
)

// TODO: mprotect / mlock this sucker and put it inside the KoL interface
type KoLRelay interface {
    LogIn(string)              error
    LogOut()                   ([]byte, error)
    SubmitChat(string, string) ([]byte, error)
    PollChat()                 ([]byte, error)
    DecodeChat([]byte)         (*ChatResponse, error)
    HandleKoLException(error)  error
    PlayerId() int64
}

type relay struct {
    UserName      string
    HttpClient    *http.Client
    SessionId     string
    PasswordHash  string
    LastSeen      string
    playerId      int64
}

func NewKoL(userName string) KoLRelay {
    cookieJar, _ := cookiejar.New(nil)
    httpClient    := &http.Client{
        Jar:           cookieJar,
        CheckRedirect: func(req *http.Request, via []*http.Request) error {
            // KoL sends the session ID Set-Cookie on a 301, so we need to
            // check all redirects for cookies.
            // This looks like a golang bug, in that the cookiejar is not
            // being updated during redirects.
            cookies := cookieJar.Cookies(req.URL)
            for i := 0; i < len(cookies); i++ {
                req.Header.Set( cookies[i].Name, cookies[i].Value )
            }
            return nil
        },
    }

    kol := &relay{
        UserName:   userName,
        HttpClient: httpClient,
        LastSeen:   "0",
        playerId:   3152049, // TODO
        PasswordHash: "",
    }
    return kol
}

func (kol *relay)PlayerId() int64 {
    return kol.playerId
}

var relay_bot_username       string
var relay_bot_password       string
var relay_bot_discord_key    string
var relay_bot_target_channel string

var passwordHashPatterns []*regexp.Regexp = []*regexp.Regexp {
    regexp.MustCompile(`name=["']?pwd["']? value=["']([^"']+)["']`),
    regexp.MustCompile(`pwd=([^&]+)`),
    regexp.MustCompile(`pwd = "([^"]+)"`),
}

var gameNameOverride sync.Map
func init() {
    rand.Seed(time.Now().UnixNano())

    // Can we connect?
    db, err := sql.Open("sqlite3", "./kol_relay.db")
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

    contents, err := ioutil.ReadFile("config.json")
    if err != nil {
        panic(err)
    }

    var i interface{}
    err = json.Unmarshal(contents, &i)
    // TODO this can't be right...
    m                       := i.(map[string]interface{})
    kol                     := m["kol"].(map[string]interface{})
    discord                 := m["discord"].(map[string]interface{})
    relay_bot_username       = kol["user"].(string)
    relay_bot_password       = kol["pass"].(string)
    relay_bot_discord_key    = discord["api_key"].(string)
    relay_bot_target_channel = discord["channel"].(string)
}

func (kol *relay) LogIn(password string) error {
    httpClient := kol.HttpClient

    loginParams := url.Values{}
    loginParams.Set("loggingin",    "Yup.")
    loginParams.Set("loginname",    kol.UserName)
    loginParams.Set("password",     password)
    loginParams.Set("secure",       "0")
    loginParams.Set("submitbutton", "Log In")

    loginBody := strings.NewReader(loginParams.Encode())
    req, err := http.NewRequest("POST", loginUrl, loginBody)
    if err != nil {
        return err
    }
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
    resp, err := httpClient.Do(req)

    if err != nil {
        return err
    }
    defer resp.Body.Close()

    body, _ := ioutil.ReadAll(resp.Body)
    for _, cookie := range httpClient.Jar.Cookies(req.URL) {
        if strings.EqualFold(cookie.Name, "PHPSESSID") {
            kol.SessionId = cookie.Value
        }
    }

    if kol.SessionId == "" {
        return errors.New("Failed to aquire session id")
    }

    responseErr := CheckResponseForErrors(resp, body)
    if responseErr != nil {
        return responseErr
    }

    // Looks like we logged in successfuly.  Try to get the pwd hash
    // and player ID
    err = kol.ResolveCharacterData()
    if err != nil {
        return err
    }

    return nil
}

func (kol *relay) LogOut() ([]byte, error) {
    httpClient := kol.HttpClient
    req, err := http.NewRequest("GET", logoutUrl, nil)
    if err != nil {
        return nil, err
    }
    req.Header.Set("Accept",          "application/json, text/javascript, */*; q=0.01")
    req.Header.Set("Accept-Encoding", "gzip")
    req.Header.Set("Refered",         "https://www.kingdomofloathing.com/mchat.php")

    resp, err := httpClient.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    body, _ := ioutil.ReadAll(resp.Body)
    return body, CheckResponseForErrors(resp, body)
}

// {"msgs":[],"last":"1468191607","delay":3000}
// {"msgs":[{"msg":"Howdy.","type":"public","mid":"1468191682","who":{"name":"Soloflex","id":"2886007","color":"black"},"format":"0","channel":"clan","channelcolor":"green","time":"1537040363"}],"last":"1468191682","delay":3000}
type KoLPlayer struct {
    Name  string `json:"name"`
    Id    interface{} `json:"id"`
    Color string `json:"color"`
}
type ChatMessage struct {
    Msg          string    `json:"msg"`
    Type         string    `json:"type"`
    Mid          interface{}    `json:"mid"`
    Who          KoLPlayer `json:"who"`
    Format       interface{}    `json:"format"`
    Channel      string    `json:"channel"`
    ChannelColor string    `json:"channelcolor"`
    Time         interface{}    `json:"time"`
}
type ChatResponse struct {
    Msgs  []ChatMessage  `json:"msgs"`
    Last  interface{}    `json:"last"`
    Delay interface{}    `json:"delay"`
}

func (kol *relay) PollChat() ([]byte, error) {
    httpClient := kol.HttpClient
    req, err := http.NewRequest("GET", fmt.Sprintf("%s?lasttime=%s&j=1", newMessageUrl, kol.LastSeen), nil)
    if err != nil {
        return nil, err
    }
    req.Header.Set("Accept",          "application/json, text/javascript, */*; q=0.01")
    req.Header.Set("Accept-Encoding", "gzip")
    req.Header.Set("Refered",         "https://www.kingdomofloathing.com/mchat.php")

    resp, err := httpClient.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    body, _ := ioutil.ReadAll(resp.Body)

    if strings.Contains(resp.Header.Get("Content-Encoding"), "gzip") {
        gr, err := gzip.NewReader(bytes.NewBuffer(body))
        defer gr.Close()
        body, err = ioutil.ReadAll(gr)
        if err != nil {
            return nil, err
        }
    }

    return body, CheckResponseForErrors(resp, body)
}

func (kol *relay)DecodeChat(jsonChat []byte) (*ChatResponse, error) {
    var jsonResponse ChatResponse
    err := json.Unmarshal(jsonChat, &jsonResponse)
    if err != nil {
        fmt.Println("The body that broke us: ", string(jsonChat))
        return nil, err
    }

    switch jsonResponse.Last.(type) {
        case string:
            kol.LastSeen = jsonResponse.Last.(string)
            break
        case float64:
            kol.LastSeen = fmt.Sprintf("%v", jsonResponse.Last)
            break
    }

    return &jsonResponse, nil
}

const (
    Disconnect = iota
    Rollover
    BadRequest
    ServerError
    Unknown
)

type KoLError struct {
    ResponseBody []byte
    ErrorMsg     string
    ErrorType    int
}

func (error *KoLError) Error() string {
    return error.ErrorMsg
}

func (kol *relay) SubmitChat(destination string, message string) ([]byte, error) {
    httpClient  := kol.HttpClient
    msg         := destination + url.QueryEscape(" " + message)
    finalUrl   := fmt.Sprintf("%s?playerid=%d&pwd=%s&j=1&graf=%s", submitMessageUrl, kol.playerId, kol.PasswordHash, msg)
    req, err := http.NewRequest("POST", finalUrl, nil)
    if err != nil {
        return nil, err
    }

    //req.Header.Set("User-Agent",      "KoL-chat-to-Discord relay")
    req.Header.Set("X-Asym-Culprit",  "Maintained by Hugmeir(#3061055)")
    req.Header.Set("X-Asym-Reason",   "Uniting /clan and the clan Discord")
    req.Header.Set("X-Asym-Source",   "https://github.com/Hugmeir/kol-relay")
    req.Header.Set("Accept-Encoding", "gzip")
    req.Header.Set("Refered",         "https://www.kingdomofloathing.com/mchat.php")

    resp, err := httpClient.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    body, _ := ioutil.ReadAll(resp.Body)

    if strings.Contains(resp.Header.Get("Content-Encoding"), "gzip") {
        gr, err := gzip.NewReader(bytes.NewBuffer(body))
        defer gr.Close()
        body, err = ioutil.ReadAll(gr)
        if err != nil {
            return nil, err
        }
    }

    return body, CheckResponseForErrors(resp, body)
}

func CheckResponseForErrors(resp *http.Response, body []byte) error {
    if resp.StatusCode >= 400 && resp.StatusCode < 500 {
        return &KoLError {
            body,
            fmt.Sprintf("KoL returned a %d; our request was broken somehow", resp.StatusCode),
            BadRequest,
        }
    } else if resp.StatusCode >= 500 {
        return &KoLError {
            body,
            fmt.Sprintf("KoL returned a %d; game is broken!", resp.StatusCode),
            ServerError,
        }
    } else if resp.StatusCode >= 300 {
        return &KoLError {
            body,
            fmt.Sprintf("KoL returned a %d; redirect spiral?!", resp.StatusCode),
            ServerError,
        }
    }

    // So this was a 200.  Check where we ended up:
    finalURL := resp.Request.URL.String()
    if strings.Contains(finalURL, "login.php") {
        // Got redirected to login.php!  That means we were disconnected.
        return &KoLError{
            body,
            "Redirected to login.php when submiting a message, looks like we got disconnected",
            Disconnect,
        }
    } else if strings.Contains(finalURL, "maint.php") {
        return &KoLError{
            body,
            "Rollover",
            Rollover,
        }
    }

    return nil
}

func (kol *relay) queryLChat() ([]byte, error) {
    httpClient := kol.HttpClient
    req, err    := http.NewRequest("GET", lChatUrl, nil)
    if err != nil {
        return nil, err
    }

    resp, err := httpClient.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    body, _ := ioutil.ReadAll(resp.Body)
    return body, CheckResponseForErrors(resp, body)
}

func (kol *relay) ResolveCharacterData() error {
    bodyBytes, err := kol.queryLChat()
    if err != nil {
        return err
    }
    body := string(bodyBytes)

    kol.PasswordHash = ""
    for _, pattern := range passwordHashPatterns {
        match := pattern.FindStringSubmatch(body)
        if match != nil && len(match) > 0 {
            kol.PasswordHash = string(match[1])
            break
        }
    }

    if kol.PasswordHash == "" {
        return errors.New("Cannot find password hash?!")
    }

    // TODO: get player ID here
    return nil
}

var globalStfu bool = false
var metaRegexp *regexp.Regexp = regexp.MustCompile("([\\\\`])")
func RelayToDiscord(dg *discordgo.Session, destChannel string, message ChatMessage) {
    if globalStfu {
        return
    }
    if !strings.Contains(message.Channel, "clan") {
        return
    }

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

    cleanedMessage = metaRegexp.ReplaceAllString(cleanedMessage, `\$1`)

    dg.ChannelMessageSend(destChannel, fmt.Sprintf("**%s**: `%s`", message.Who.Name, cleanedMessage))
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

// This should be [\p{Latin1}\p{ASCII}], but no such thing in golang
var nonLatin1Re *regexp.Regexp = regexp.MustCompile(`[^\x00-\xff]`)
func sanitizeForKoL (content string) string {

    // KoL chat only accepts the latin1 range:
    content = nonLatin1Re.ReplaceAllString(content, ``)

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

var verifyRe *regexp.Regexp = regexp.MustCompile(`(?i)^\s*verify(?:\s* me)?: ([0-9]{10,})`)
func HandleDM(s *discordgo.Session, m *discordgo.MessageCreate) {
    verifyMatches := verifyRe.FindStringSubmatch(m.Content)
    if len(verifyMatches) > 0 {
        HandleVerification(s, m, verifyMatches[1])
    } else {
        s.ChannelMessageSend(m.ChannelID, "<some funny message about not understanding what you mean>");
    }
}

var sqliteInsert sync.Mutex
func InsertNewNickname(discordId string, nick string) {
    sqliteInsert.Lock()
    defer sqliteInsert.Unlock()
    db, err := sql.Open("sqlite3", "./kol_relay.db")
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
func HandleVerification(s *discordgo.Session, m *discordgo.MessageCreate, verificationCode string) {
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

func IsKoLDM(message ChatMessage) bool {
    if message.Type == "private" {
        return true
    }
    return false
}

var firstVerifyRe *regexp.Regexp = regexp.MustCompile(`(?i)^\s*verify(?:\s* me)?!?`)
func HandleKoLDM(kol KoLRelay, message ChatMessage) {
    if !firstVerifyRe.MatchString(message.Msg) {
        return
    }

    sender := message.Who
    var senderId string
    switch sender.Id.(type) {
        case string:
            senderId, _ = sender.Id.(string)
            break
        case int64:
            senderId = strconv.FormatInt(sender.Id.(int64), 10)
            break
        case float64:
            senderId = strconv.FormatInt(int64(sender.Id.(float64)), 10)
            break
    }

    _, ok := verificationsPending.Load("User:" + message.Who.Name);
    if ok {
        kol.SubmitChat("/msg " + senderId, "Already sent you a code, you must wait 5 minutes to generate a new one")
        return
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
        regexp.MustCompile(`(?i)^\s*RelayBot,?\s+tell\s+me\s+about\s+(.+)`),
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

func HandleMessageFromDiscord(s *discordgo.Session, m *discordgo.MessageCreate, fromDiscord *os.File, discordToKoL chan<- string) {
    if m.Author.ID == s.State.User.ID {
        // Ignore ourselves
        return
    }

    if m.Author.Bot {
        // Ignore other bots
        // yes, I am hardcoding /baleet Odebot.  Take that!
        return
    }

    if m.ChannelID != relay_bot_target_channel {
        dm, _ := ComesFromDM(s, m)
        if dm {
            HandleDM(s, m)
        }
        return // someone spoke in general, ignore
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
        return
    }

    if globalStfu {
        return // respect the desire for silence
    }

    go RandomBullshit(s, m)

    author    := sanitizeForKoL(ResolveNickname(s, m))
    msgForKoL := sanitizeForKoL(m.Content)
    finalMsg  := author + ": " + msgForKoL
    if len(finalMsg) > 200 {
        // Hm..
        if len(finalMsg + author) < 300 {
            // Just split it
            discordToKoL <- finalMsg[:150] + "..."
            discordToKoL <- author + ": ..." + finalMsg[150:]
            return
        }
        // Too long!
        s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Brevity is the soul of wit, %s.  That message was too long, so it will not get relayed.", author))
        return
    }
    discordToKoL <- finalMsg
}

func (kol *relay)HandleKoLException(err error) error {
    if err == nil {
        return nil
    }

    kolError, ok := err.(*KoLError)
    if !ok {
        return err
    }

    if kolError.ErrorType == Rollover {
        fmt.Println("Looks like we are in rollover.  Just shut down.")
        return err
    } else if kolError.ErrorType == Disconnect {
        fmt.Println("Looks like we were disconnected.  Try to reconnect!")
        err = kol.LogIn(relay_bot_password)
        if err != nil {
            return err
        }
    } else if kolError.ErrorType == BadRequest {
        // Weird.  Just log it.
        fmt.Println("Exception due to bad request.  Logging it and ignoring it: ", kolError)
        return nil
    } else if kolError.ErrorType == ServerError {
        // Weird.  Just log it.
        fmt.Println("Server is having a bad time.  Logging it and ignoring it: ", kolError)
        return nil
    } else { // Some other error
        return err
    }

    return nil
}

func main() {
    discordToKoL := make(chan string)

    fromDiscordLogfile := "/var/log/kol-relay/from_discord.log"
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

    // Called when the bot sees a message on discord
    dg := NewDiscordConnection(relay_bot_discord_key)
    dg.AddHandler(func (s *discordgo.Session, m *discordgo.MessageCreate) {
        HandleMessageFromDiscord(s, m, fromDiscord, discordToKoL)
    })

    kol := NewKoL(relay_bot_username)
    err  = kol.LogIn(relay_bot_password)
    if err != nil {
        panic(err)
    }

    // Poll every 3 seconds:
    ticker := time.NewTicker(3*time.Second)
    defer ticker.Stop()

    awaitTicker := time.NewTicker(3*time.Minute)
    defer awaitTicker.Stop()

    go func() {
        responseRaw, err := kol.SubmitChat("/msg hugmeir", "oh hai creator")
        if err != nil {
            fmt.Println("Cannot send initial message, something has gone wrong: %v", err)
            panic(err)
        }
        fmt.Fprintf(fromKoL, "%s %d [RESPONSE]: %s\n", time.Now().Format(time.RFC3339), os.Getpid(), string(responseRaw))
        for { // just an infinite loop
            // select waits until ticker ticks over, then runs this code
            select {
                case msg := <-discordToKoL:
                    // First, disarm the away ticker and re-arm it:
                    awaitTicker.Stop()
                    awaitTicker = time.NewTicker(3*time.Minute)
                    responseRaw, err := kol.SubmitChat("/clan", msg)
                    if err != nil {
                        fatalError := kol.HandleKoLException(err)
                        if fatalError != nil {
                            fmt.Println("Got an error submitting to kol?!")
                            panic(fatalError)
                        }

                        // Exception was handled, so retry:
                        responseRaw, err = kol.SubmitChat("/clan", msg)
                        if err != nil {
                            // Well, we tried, silver star.  Die:
                            panic(err)
                        }
                    }
                    fmt.Fprintf(fromKoL, "%s %d [RESPONSE]: %s\n", time.Now().Format(time.RFC3339), os.Getpid(), string(responseRaw))
                    break
                case <-awaitTicker.C:
                    _, err := kol.SubmitChat("/who", "clan")
                    fatalError := kol.HandleKoLException(err)
                    if fatalError != nil {
                        panic(fatalError)
                    }
                    break
            }
        }
    }()

    go func() {
        for { // just an infinite loop
            // select waits until ticker ticks over, then runs this code
            select {
            case <-ticker.C:
                rawChatReponse, err := kol.PollChat()
                if err != nil {
                    fatalError      := kol.HandleKoLException(err)
                    if fatalError != nil {
                        // Probably rollover?
                        panic(fatalError)
                    }
                    fmt.Println("Polling KoL had some error we are now ignoring: ", err)
                    continue
                }

                // Dumb heuristics!  If it contains msgs:[], it's an empty response,
                // so don't log it... unless it also contains "output":, in which case
                // there might be an error in there somewhere.
                chatReponseString := string(rawChatReponse)
                if !strings.Contains(chatReponseString, `"msgs":[]`) || strings.Contains(chatReponseString, `"output":`) {
                    fmt.Fprintf(fromKoL, "%s: %s\n", time.Now().Format(time.RFC3339), string(rawChatReponse))
                }

                chatResponses, err := kol.DecodeChat(rawChatReponse)
                if err != nil {
                    fmt.Println("Could not decode chat from KoL, ignoring it for now ", err)
                    continue
                }

                for i := 0; i < len(chatResponses.Msgs); i++ {
                    message := chatResponses.Msgs[i]
                    sender := message.Who
                    var senderId int64
                    switch sender.Id.(type) {
                        case string:
                            senderId, _ = strconv.ParseInt(sender.Id.(string), 10, 64)
                            break
                        case int64:
                            senderId = sender.Id.(int64)
                            break
                        case float64:
                            senderId = int64(sender.Id.(float64))
                            break
                    }

                    if senderId == kol.PlayerId() {
                        continue
                    }

                    if IsKoLDM(message) {
                        HandleKoLDM(kol, message)
                        continue
                    }

                    RelayToDiscord(dg, relay_bot_target_channel, message)
                }
            }
        }
    }()

    // Cleanly close down the Discord session.
    defer dg.Close()
    // And disconnect from KoL
    defer kol.LogOut()

    fmt.Println("Bot is now running.  Press CTRL-C to exit.")
    sc := make(chan os.Signal, 1)
    signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
    <-sc
}


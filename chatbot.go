package main
import (
    "os"
    "os/signal"
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
    "encoding/json"
    "compress/gzip"
    "github.com/bwmarrin/discordgo"
)

var base_url           string = "https://www.kingdomofloathing.com/"
var login_url          string = base_url + "login.php"
var new_message_url    string = base_url + "newchatmessages.php"
var submit_message_url string = base_url + "submitnewchat.php"

// TODO: mprotect / mlock this sucker and put it inside the KoL interface
var kol_password string
type KoLRelay interface {
    LogIn(string)              error
    SubmitChat(string, string) ([]byte, error)
    PollChat()                 ([]byte, error)
    DecodeChat([]byte)         (*ChatResponse, error)
    PlayerId() int64
}

type relay struct {
    username      string
    http_client   *http.Client
    session_id    string
    password_hash string
    last_seen     string
    player_id     int64
}

func NewKoL(username string, password string) KoLRelay {
    cookie_jar, _ := cookiejar.New(nil)
    http_client   := &http.Client{
        Jar:           cookie_jar,
        CheckRedirect: func(req *http.Request, via []*http.Request) error {
            // KoL sends the session ID Set-Cookie on a 301, so we need to
            // check all redirects for cookies.
            // This looks like a golang bug, in that the cookiejar is not
            // being updated during redirects.
            cookies := cookie_jar.Cookies(req.URL)
            for i := 0; i < len(cookies); i++ {
                req.Header.Set( cookies[i].Name, cookies[i].Value )
            }
            return nil
        },
    }

    kol_password = password // TODO
    kol := &relay{
        username:    username,
        http_client: http_client,
        last_seen:   "0",
        player_id:   3152049, // TODO
        password_hash: "",
    }
    err := kol.LogIn(password)
    if err != nil {
        panic(err)
    }

    return kol
}

func (kol *relay)PlayerId() int64 {
    return kol.player_id
}

var relay_bot_username       string
var relay_bot_password       string
var relay_bot_discord_key    string
var relay_bot_target_channel string

var PASSWORD_HASH_PATTERNS []*regexp.Regexp = []*regexp.Regexp {
    regexp.MustCompile(`name=["']?pwd["']? value=["']([^"']+)["']`),
    regexp.MustCompile(`pwd=([^&]+)`),
    regexp.MustCompile(`pwd = "([^"]+)"`),
}

func initialize() {
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
    http_client := kol.http_client

    login_params := url.Values{}
    login_params.Set("loggingin",    "Yup.")
    login_params.Set("loginname",    kol.username)
    login_params.Set("password",     kol_password)
    login_params.Set("secure",       "0")
    login_params.Set("submitbutton", "Log In")

    login_body := strings.NewReader(login_params.Encode())
    req, err := http.NewRequest("POST", login_url, login_body)
    if err != nil {
        return err
    }
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
    resp, err := http_client.Do(req)

    if err != nil {
        return err
    }
    defer resp.Body.Close()

    //body, _ := ioutil.ReadAll(resp.Body)
    for _, cookie := range http_client.Jar.Cookies(req.URL) {
        if strings.EqualFold(cookie.Name, "PHPSESSID") {
            kol.session_id = cookie.Value
        }
    }

    if kol.session_id == "" {
        return errors.New("Failed to aquire session id")
    }

    err = kol.resolve_password_hash()
    if err != nil {
        return err
    }

    return nil
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
    Type         interface{}    `json:"type"`
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
    http_client := kol.http_client
    req, err := http.NewRequest("GET", fmt.Sprintf("%s?lasttime=%s&j=1", new_message_url, kol.last_seen), nil)
    if err != nil {
        return nil, err
    }
    req.Header.Set("Accept",          "application/json, text/javascript, */*; q=0.01")
    req.Header.Set("Accept-Encoding", "gzip")
    req.Header.Set("Refered",         "https://www.kingdomofloathing.com/mchat.php")

    resp, err := http_client.Do(req)
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

    return body, nil
}

func (kol *relay)DecodeChat(json_chat []byte) (*ChatResponse, error) {
    var json_response ChatResponse
    err := json.Unmarshal(json_chat, &json_response)
    if err != nil {
        fmt.Println("The body that broke us: ", string(json_chat))
        return nil, err
    }

    switch json_response.Last.(type) {
        case string:
            kol.last_seen = json_response.Last.(string)
            break
        case float64:
            kol.last_seen = fmt.Sprintf("%v", json_response.Last)
            break
    }

    return &json_response, nil
}

func (kol *relay) SubmitChat(destination string, message string) ([]byte, error) {
    http_client := kol.http_client
    msg         := destination + " " + url.QueryEscape(message)
    final_url   := fmt.Sprintf("%s?playerid=%d&pwd=%s&j=1&graf=%s", submit_message_url, kol.player_id, kol.password_hash, msg)
    req, err := http.NewRequest("POST", final_url, nil)
    if err != nil {
        return nil, err
    }

    req.Header.Set("Accept-Encoding", "gzip")
    req.Header.Set("Refered",         "https://www.kingdomofloathing.com/mchat.php")

    resp, err := http_client.Do(req)
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

    return body, nil
}

func (kol *relay) ping_lchat_for_data() ([]byte, error) {
    http_client := kol.http_client
    req, err    := http.NewRequest("GET", base_url + "lchat.php", nil)
    if err != nil {
        return nil, err
    }

    resp, err := http_client.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    body_bytes, _ := ioutil.ReadAll(resp.Body)
    return body_bytes, nil
}

func (kol *relay) resolve_password_hash() error {
    body_bytes, err := kol.ping_lchat_for_data()
    if err != nil {
        fmt.Println("Cannot resolve pwd hash")
        panic(err)
    }
    body := string(body_bytes)

    for _, pattern := range PASSWORD_HASH_PATTERNS {
        match := pattern.FindStringSubmatch(body)
        if match != nil && len(match) > 0 {
            kol.password_hash = string(match[1])
            return nil
        }
    }

    return errors.New("Cannot find password hash?!")
}

var global_stfu bool = false
func relay_to_discord(dg *discordgo.Session, message ChatMessage) {
    if global_stfu {
        return
    }
    if !strings.Contains(message.Channel, "clan") {
        return
    }

    raw_message     := message.Msg;
    cleaned_message := html.UnescapeString(raw_message)
    if strings.HasPrefix(cleaned_message, "<") {
        // golden text, chat effects, etc.
        tokens := html.NewTokenizer(strings.NewReader(cleaned_message))
        cleaned_message = ""
        loop:
        for {
            tt := tokens.Next()
            switch tt {
            case html.ErrorToken:
                break loop
            case html.TextToken:
                cleaned_message = cleaned_message + string(tokens.Text())
            }
            // TODO: could grab colors & apply them in markdown
        }
    }

    meta_characters := regexp.MustCompile("([\\\\`])")
    cleaned_message = meta_characters.ReplaceAllString(cleaned_message, `\$1`)

    dg.ChannelMessageSend(relay_bot_target_channel, fmt.Sprintf("**%s**: `%s`", message.Who.Name, cleaned_message))
}

func open_discord_connection(on_message func(*discordgo.Session, *discordgo.MessageCreate)) *discordgo.Session {
    dg, err := discordgo.New("Bot " + relay_bot_discord_key)

    err = dg.Open()
    if err != nil {
        panic(err)
    }
    dg.AddHandler(on_message)

    return dg
}

func resolve_nickname(s *discordgo.Session, m *discordgo.MessageCreate) string {
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
            return member.Nick
        }
        break;
    }

    return m.Author.Username // fallback
}

// This should be [\p{Latin1}\p{ASCII}], but no such thing in golang
var non_latin_1_re *regexp.Regexp = regexp.MustCompile(`[^\x00-\xff]`)
func sanitize_message_for_kol (s *discordgo.Session, m *discordgo.MessageCreate) string {
    author  := resolve_nickname(s, m)
    content := m.Content

    // KoL chat only accepts the latin1 range:
    content = non_latin_1_re.ReplaceAllString(content, ``)

    // KoL chat only accepts latin1, so encode before sending:
    encoded, err := charmap.ISO8859_1.NewEncoder().String(content)
    if err != nil {
        fmt.Printf("Failed to encode message: %v\n", err)
        encoded = content
    }

    return author + ": " + encoded
}

func handle_message_from_discord(s *discordgo.Session, m *discordgo.MessageCreate, from_discord *os.File, discord_to_kol chan<- string) {
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
        return // someone spoke in general, ignore
    }

    msg_as_json, _ := json.Marshal(m)
    fmt.Fprintf(from_discord, "%s: %s\n", time.Now().Format(time.RFC3339), msg_as_json)

    if m.Content == "" {
        // Empty message
        // We get here when someone sends a file/picture etc
        // with no message body.  Just skip it.
        return
    }

    if m.Content == "RelayBot, stfu" {
        // We have been asked to quit it, so do!
        global_stfu = true
        return
    }

    if m.Content == "RelayBot, spam on" {
        global_stfu = false
        return
    }

    if global_stfu {
        return // respect the desire for silence
    }

    message_for_kol := sanitize_message_for_kol(s, m)
    discord_to_kol <- message_for_kol
}

func main() {
    initialize()
    discord_to_kol := make(chan string)

    from_discord_logfile := "/var/log/kol-relay/from_discord.log"
    from_kol_logfile     := "/var/log/kol-relay/from_kol.log"

    from_discord, err := os.OpenFile(from_discord_logfile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
    if err != nil {
        panic(err)
    }
    defer from_discord.Close()

    from_kol, err := os.OpenFile(from_kol_logfile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
    if err != nil {
        panic(err)
    }
    defer from_kol.Close()

    // Called when the bot sees a message on discord
    dg := open_discord_connection(func (s *discordgo.Session, m *discordgo.MessageCreate) {
        handle_message_from_discord(s, m, from_discord, discord_to_kol)
    })
    kol := NewKoL(relay_bot_username, relay_bot_password)

    // Poll every 3 seconds:
    ticker := time.NewTicker(3*time.Second)
    defer ticker.Stop()

    away_ticker := time.NewTicker(3*time.Minute)
    defer away_ticker.Stop()

    go func() {
        response_raw, err := kol.SubmitChat("/msg hugmeir", "oh hai creator")
        if err != nil {
            fmt.Println("Cannot send initial message, something has gone wrong: %v", err)
            panic(err)
        }
        fmt.Fprintf(from_kol, "%s %d [RESPONSE]: %s\n", time.Now().Format(time.RFC3339), os.Getpid(), string(response_raw))
        for { // just an infinite loop
            // select waits until ticker ticks over, then runs this code
            select {
                case msg := <-discord_to_kol:
                    // First, disarm the away ticker and re-arm it:
                    away_ticker.Stop()
                    away_ticker = time.NewTicker(3*time.Minute)
                    response_raw, err := kol.SubmitChat("/clan", msg)
                    if err != nil {
                        fmt.Println("Got an error submitting to kol?!")
                        continue
                    }
                    fmt.Fprintf(from_kol, "%s %d [RESPONSE]: %s\n", time.Now().Format(time.RFC3339), os.Getpid(), string(response_raw))
                    break
                case <-away_ticker.C:
                    kol.SubmitChat("/who", "clan")
                    break
            }
        }
    }()

    go func() {
        for { // just an infinite loop
            // select waits until ticker ticks over, then runs this code
            select {
            case <-ticker.C:
                raw_chat_response, err := kol.PollChat()
                if err != nil {
                    // Might as well assume that we git disconnected
                    err = kol.LogIn(relay_bot_password)
                    if err != nil {
                        // Probably rollover?
                        panic(err)
                    }
                    fmt.Println("Polling KoL had some error we are now ignoring: ", err)
                    continue
                }

                // Dumb heuristics!  If it contains msgs:[], it's an empty response,
                // so don't log it... unless it also contains "output":, in which case
                // there might be an error in there somewhere.
                str_chat_response := string(raw_chat_response)
                if !strings.Contains(str_chat_response, `"msgs":[]`) || strings.Contains(str_chat_response, `"output":`) {
                    fmt.Fprintf(from_kol, "%s: %s\n", time.Now().Format(time.RFC3339), string(raw_chat_response))
                }

                chat_response, err := kol.DecodeChat(raw_chat_response)
                if err != nil {
                    panic(err)
                }

                for i := 0; i < len(chat_response.Msgs); i++ {
                    message := chat_response.Msgs[i]
                    sender := message.Who
                    var sender_id int64
                    switch sender.Id.(type) {
                        case string:
                            sender_id, _ = strconv.ParseInt(sender.Id.(string), 10, 64)
                            break
                        case int64:
                            sender_id = sender.Id.(int64)
                            break
                        case float64:
                            sender_id = int64(sender.Id.(float64))
                            break
                    }

                    if sender_id == kol.PlayerId() {
                        continue
                    }

                    relay_to_discord(dg, message)
                }
            }
        }
    }()

    fmt.Println("Bot is now running.  Press CTRL-C to exit.")
    sc := make(chan os.Signal, 1)
    signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
    <-sc

    // Cleanly close down the Discord session.
    dg.Close()
    // TODO: disconnect from KoL!
}


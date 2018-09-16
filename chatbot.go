package main
import (
    "os"
    "os/signal"
    "regexp"
    "fmt"
    "time"
    "syscall"
    "net/url"
    "golang.org/x/net/html"
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
    HttpClient()       *http.Client
    LogIn(string)      error
    PollChat()         (*ChatResponse, error)
    SubmitChat(string) error
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
    }
    kol.LogIn(password)

    return kol
}

func (kol relay) HttpClient() *http.Client {
    return kol.http_client
}

var relay_bot_username       string
var relay_bot_password       string
var relay_bot_discord_key    string
var relay_bot_target_channel string
var password_hash            string

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

func (kol relay) LogIn(password string) error {
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

func (kol relay) PollChat() (*ChatResponse, error) {
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

    var json_response ChatResponse
    err = json.Unmarshal(body, &json_response)
    if err != nil {
        fmt.Println("The body that broke us: ", string(body))
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

func (kol relay) SubmitChat(message string) error {
    http_client := kol.http_client
    playerId    := kol.player_id
    req, err := http.NewRequest("POST", fmt.Sprintf("%s?playerid=%s&pwd=%s&j=1&graf=%s", submit_message_url, playerId, password_hash, url.QueryEscape(message)), nil)
    if err != nil {
        return err
    }

    req.Header.Set("Accept-Encoding", "gzip")
    req.Header.Set("Refered",         "https://www.kingdomofloathing.com/mchat.php")

    resp, err := http_client.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    body, _ := ioutil.ReadAll(resp.Body)

    if strings.Contains(resp.Header.Get("Content-Encoding"), "gzip") {
        gr, err := gzip.NewReader(bytes.NewBuffer(body))
        defer gr.Close()
        body, err = ioutil.ReadAll(gr)
        if err != nil {
            return err
        }
    }

    //fmt.Println("submit response: ", string(body))

    return nil
}

func (kol relay) resolve_password_hash() error {
    http_client := kol.http_client
    req, err    := http.NewRequest("GET", base_url + "lchat.php", nil)
    if err != nil {
        return err
    }

    resp, err := http_client.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    body_bytes, _ := ioutil.ReadAll(resp.Body)
    body := string(body_bytes)
    for _, pattern := range PASSWORD_HASH_PATTERNS {
        match := pattern.FindStringSubmatch(body)
        if match != nil && len(match) > 0 {
            kol.password_hash = match[1]
            return nil
        }
    }
/*
    match := HASH_PATTERN_1.FindStringSubmatch(body)
    if match != nil && len(match) > 0 {
        return match[1]
    }

    match = HASH_PATTERN_2.FindStringSubmatch(body)
    if match != nil && len(match) > 0 {
        return match[1]
    }

    match = HASH_PATTERN_3.FindStringSubmatch(body)
    if match != nil && len(match) > 0 {
        return match[1]
    }
*/

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

    cleaned_message = strings.Replace(cleaned_message, "`", "\\`", -1)

    dg.ChannelMessageSend(relay_bot_target_channel, fmt.Sprintf("**%s**: `%s`", message.Who.Name, cleaned_message))
}

func open_discord_connection() *discordgo.Session {
    dg, err := discordgo.New("Bot " + relay_bot_discord_key)

    err = dg.Open()
    if err != nil {
        panic(err)
    }
    dg.AddHandler(on_message_from_discord)

    return dg
}

// Called when the bot sees a message on discord
func on_message_from_discord(s *discordgo.Session, m *discordgo.MessageCreate) {
    if m.Content == "RelayBot, stfu" {
        // We have been asked to quit it, so do!
        global_stfu = true
        return
    }
    if m.Content != "RelayBot, spam on" {
        global_stfu = false
        return
    }
}

func main() {
    initialize()
    kol := NewKoL(relay_bot_username, relay_bot_password)

    dg := open_discord_connection()

    // Poll every 3 seconds:
    ticker := time.NewTicker(3*time.Second)
    defer ticker.Stop()

    away_ticker := time.NewTicker(2*time.Minute)
    defer away_ticker.Stop()

    go func() {
        kol.SubmitChat("/msg hugmeir oh hai creator")
        for { // just an infinite loop
            // select waits until ticker ticks over, then runs this code
            select {
                case <-away_ticker.C:
                    kol.SubmitChat("/who clan")
            }
        }
    }()

    go func() {
        for { // just an infinite loop
            // select waits until ticker ticks over, then runs this code
            select {
            case <-ticker.C:
                chat_response, err := kol.PollChat()
                if err != nil {
                    fmt.Println("Polling KoL had some error we are now ignoring: ", err)
                    continue
                }
                for i := 0; i < len(chat_response.Msgs); i++ {
                    relay_to_discord(dg, chat_response.Msgs[i])
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


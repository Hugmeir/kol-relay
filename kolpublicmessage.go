package main
import (
    "regexp"
    "fmt"
    "golang.org/x/net/html"

    "strings"
    "bytes"
    "sort"

    "github.com/Hugmeir/kolgo"

    "net/http"
    "net/url"
    "strconv"
)

var discordMeta = regexp.MustCompile("([\\_*~`])")
func EscapeDiscordMetaCharacters(s string) string {
    s = discordMeta.ReplaceAllString(s, "\\$1")
    return s
}

var linkMatcher = regexp.MustCompile(`(?i)<a target=_blank href="([^"]+)"[^>]*><font[^>]+>[^<]+<[^>]+><\\?/a>`)
func FixMangledChatLinks(a string) (string, []string) {
    s := []byte(a)

    urls := []string{}
    for max := 10; max > 1; max-- {
        loc := linkMatcher.FindSubmatchIndex(s);
        if len(loc) <= 0 {
            // No matches!
            break
        }

        // Grab the url first
        urlRaw := make([]byte, 0, loc[3] - loc[2])
        urlRaw  = append(urlRaw, s[loc[2]:loc[3]]...)
        urls = append(urls, string(urlRaw))

        // Now get rid of the whole <a> eyesore:
        // The following is the go way of doing this
        // s = s[:loc[0]] + s[loc[1]:]
        // One day...
        s = s[:loc[0] + copy(s[loc[0]:], s[loc[1]+1:])]

        // Now try replacing the shitty split url with the fixed version.
        buffer := bytes.NewBufferString("")
        for idx, b := range urlRaw {
            q := regexp.QuoteMeta(string(b))
            if idx != 0 {
                buffer.WriteString(`\s*`)
            }
            buffer.WriteString(q)
        }
        urlRe, err := regexp.Compile(buffer.String())
        if err == nil {
            // If it failed to compile, meh, just ignore it; otherwise,
            // use the regex we just created to replace the broken urls
            s = urlRe.ReplaceAll(s, urlRaw)
        } else {
            fmt.Println("Regexp failed to compile with ", err)
        }
    }

    return string(s), urls
}

var setEmpty = []string{
    "2007",
    "2008",
    "2009",
    "2010",
    "2012",
    "2025",
    "4007",
    "4008",
    "4019",
    "6003",
    "6004",
    "6005",
    "6006",
    "6007",
    "6008",
    "6009",
    "6010",
    "6011",
    "6012",
    "6013",
    "6014",
    "6015",
    "6016",
    "6017",
    "6018",
    "6027",
    "6045",
}

var baseBuffs = map[string]int{
    "2007": 600,
    "2008": 600,
    "2009": 600,
    "2010": 600,
    "2012": 600,
    "2025": 600,
    "4007": 600,
    "4008": 600,
    "4019": 600,
    "6006": 600,
    "6010": 600,
}

const BuffyUrl = "https://kol.obeliks.de/buffbot/buff"
func RequestBuffsFor(who string) {
    BuffyRequest(who, baseBuffs)
}

func RequestOdeFor(who string) {
    buffs := map[string]int{
        "6014": 50,
    }
    BuffyRequest(who, buffs)
}

func BuffyRequest(who string, wantedBuffs map[string]int) {
    client := &http.Client{}
    p := url.Values{}

    for _, x := range setEmpty {
        p.Set(x, "")
    }

    p.Set("target",    who)
    p.Set("authToken", "")
    for buffID, turns := range wantedBuffs {
        p.Set(buffID, strconv.Itoa(turns))
    }


    buffs    := strings.NewReader(p.Encode())
    req, err := http.NewRequest("POST", BuffyUrl, buffs)
    if err != nil {
        fmt.Println(err)
        return
    }

    req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
    resp, err := client.Do(req)
    if err != nil {
        fmt.Println(err)
        return
    }
    defer resp.Body.Close()
}

var safariMatcher = regexp.MustCompile(`(?i)<i title="([^"]*)">([^<]+)</i>`)
func ResolveChatEffects (s string) string {
    s  = safariMatcher.ReplaceAllStringFunc(s, func (inner string) string {
        m := safariMatcher.FindStringSubmatch(inner)
        return fmt.Sprintf("*%s* (%s)", strings.Replace(m[2], ` `, ``, -1), m[1])
    })
    return s
}
var goldenGumMatcher  = regexp.MustCompile(`(?i)<span[^>]*>([^<]+)</span>`)
var holidayFunMatcher = regexp.MustCompile(`(?i)<font[^>]*>([^<]+)</font>`)
func RemoveChatColors(s string) string {
    // Vivala:
    s = strings.Replace(s, `<font color=red><b>v</b></font>`, `**v**`, -1)
    s = strings.Replace(s, `<font color=red><b>V</b></font>`, `**V**`, -1)

    s = goldenGumMatcher.ReplaceAllString(s, `$1`)
    s = holidayFunMatcher.ReplaceAllString(s, `$1`)
    s = strings.Replace(s, `<b>`,  ``, -1) // Pirate Bellow
    s = strings.Replace(s, `</b>`, ``, -1)
    return s
}

var effectMatcher  = regexp.MustCompile(`(?i)<img src="[^"]+12x12(heart|skull|snowman)\.[^"]+"[^>]*>`)
var slashMeMatcher = regexp.MustCompile(`(?i)\A(?:<b>)?<i><a target=mainpane href=[^>]+>(?:<font color[^>]+>)?([^<]+)(?:<\\?/b>)?(?:<\\?/font>)?<\\?/a>(.+)<\\?/i>\z`)
var captureItalics = regexp.MustCompile(`(?i)<i>((?:[^<]+|<\s*/?\s*[^i])+)\s*</i>`)
var captureExtraLinks = regexp.MustCompile(`(?i)<a[^>]*>([^<]+)</a>`)
var effectToCmdDefaults  map[string]string = map[string]string{
    // Defaults:
    "heart": `🖤`,
    "skull": `☠`,
    "snowman": "\u26C4", // SNOWMAN WITHOUT SNOW
    "?": "",
}

var commentMatcher = regexp.MustCompile(`(?i)<!--(?:[^-]*)-->`)
func RemoveHtmlComments(s string) string {
    s = commentMatcher.ReplaceAllString(s, ``)
    return s
}
func (bot *Chatbot) HandleKoLPublicMessage(message kolgo.ChatMessage, effectToCmd map[string]string) (string, error) {
    preparedMessage := message.Msg
    preparedSender  := fmt.Sprintf("**%s**: ", EscapeDiscordMetaCharacters(message.Who.Name))

    messagePrepend := []string{}
    messageAppend  := []string{}

    preparedMessage, urls := FixMangledChatLinks(preparedMessage)
    preparedMessage = RemoveHtmlComments(preparedMessage)

    if strings.HasPrefix(preparedMessage, "<") {
        // golden text, chat effects, etc.
        seen := make(map[string]bool, 3)
        preparedMessage = effectMatcher.ReplaceAllStringFunc(preparedMessage, func(t string) string {
            wrapperType := "?"
            if strings.Contains(t, `heart`) {
                wrapperType = `heart`   // LOV Emotionizer
            } else if strings.Contains(t, `skull`) {
                wrapperType = `skull`   // Pirate Bellow
            } else if strings.Contains(t, `snowman`) {
                wrapperType = `snowman` // Aggressive Carrot
            }

            if _, ok := seen[wrapperType]; ok {
                return ``
            }

            c, ok := effectToCmd[wrapperType]
            if !ok {
                c = effectToCmdDefaults[wrapperType]
            }

            messagePrepend = append(messagePrepend, c)
            if wrapperType != `snowman` {
                messageAppend = append(messageAppend, c)
            }
            seen[wrapperType] = true
            return ``
        })

        if meMatch := slashMeMatcher.FindStringSubmatch(preparedMessage); len(meMatch) > 0 {
            // /me foo
            messagePrepend = append(messagePrepend, `_`)
            messageAppend  = append(messageAppend,  `_`)
            preparedSender  = fmt.Sprintf("**%s**", EscapeDiscordMetaCharacters(meMatch[1]))
            preparedMessage = " " + meMatch[2] // message WITHOUT the username
        }
    }

    // Sigh... Discord has SPECIAL ESCAPING EXCEPTIONS for urls.
    // Consider this: https://foo.com/foo_bar_baz
    // In normal markdown, that would leave the "bar" in italics.
    // Discord grabs anything that looks like an url and does not
    // apply markdown to them.
    //
    // Neat, right?  Except, it also has EXTRA MAGICAL BEHAVIOR,
    // so that if you properly escape the url, ala
    //  https://foo.com/foo\_bar\_baz
    // What actually comes out is this horrible mess:
    //  https://foo.com/foo/_bar/_baz
    // So we need to escape metacharacters... except in urls.  Sigh.
    if len(urls) > 0 {
        sort.Slice(urls, func(i, j int) bool { return len(urls[i]) > len(urls[j]) })
        skip := [][]int{}
        b    := []byte(preparedMessage)
        f    := make([]byte, 0, len(b)*2)

        for _, url := range urls {
            offset := 0
            for {
                if offset >= len(b)-1 {
                    break
                }
                foundFromOffset := bytes.Index(b[offset:], []byte(url))
                if foundFromOffset == -1 {
                    break
                }
                skip = append(skip, []int{offset + foundFromOffset, offset + foundFromOffset + len(url)})
                offset = offset + foundFromOffset + len(url)
            }
        }

        // Sort the indexes
        sort.Slice(skip, func(i, j int) bool { return skip[i][0] < skip[j][0] })

        start := 0
        for _, t := range skip {
            // Duplicate strings
            if start > t[0] {
                continue
            }

            before := b[start:t[0]]
            before = discordMeta.ReplaceAll(before, []byte("\\$1"))
            f = append(f, before...)
            f = append(f, b[t[0]:t[1]]...)
            start = t[1]
        }

        if start < len(b) {
            tail := b[start:]
            tail  = discordMeta.ReplaceAll(tail, []byte("\\$1"))
            f = append(f, tail...)
        }
        preparedMessage = string(f)
    } else {
        preparedMessage = EscapeDiscordMetaCharacters(preparedMessage)
    }
    preparedMessage = captureExtraLinks.ReplaceAllString(preparedMessage, `$1`)

    for i := 1; i <= 2; i++ {
        preparedMessage = ResolveChatEffects(preparedMessage)
        preparedMessage = RemoveChatColors(preparedMessage)
        preparedMessage = captureItalics.ReplaceAllString(preparedMessage, `*$1*`)
    }

    // the very *last* thing we do is unescape html meta characters;
    // putting it earlier breaks all the other escapes.
    preparedMessage = html.UnescapeString(preparedMessage)

    finalMsg := fmt.Sprintf("%s%s", preparedSender, preparedMessage)

    for _, wrap := range messagePrepend {
        finalMsg = wrap + finalMsg
    }
    for _, wrap := range messageAppend {
        finalMsg = finalMsg + wrap
    }

    if message.Channel != "clan" {
        finalMsg = fmt.Sprintf("[%s] %s", message.Channel, finalMsg)
    }

    go func() {
        ode   := regexp.MustCompile(`(?i)\A(?:(?:\s*\*\s*)?(?:pl(?:ease|s|z)\s+)?(?:ode|beer|booze)(?:\s*[^ ]*)?|let the booze flow|let's get tipsy)\z`)
        buffs := regexp.MustCompile(`(?i)\A(?:\s*\*\s*)?(?:pl(?:ease|s|z)\s+)?buff(?:s?(?:\s*[^ ]*)?)\z`)
        bots  := regexp.MustCompile(`(?i)\Abots\??\z`)
        shitBags := regexp.MustCompile(`(?i)bitch`)

        if shitBags.MatchString(preparedMessage) {
            return
        } else if ode.MatchString(preparedMessage) {
            RequestOdeFor(message.Who.Name)
        } else if buffs.MatchString(preparedMessage) {
            RequestBuffsFor(message.Who.Name)
        } else if message.Channel == "clan" && bots.MatchString(preparedMessage) {
            bot.KoL.SendMessage("/clan", `Will immediately respond with fries/robin/thin.  You can also PM me with "hold" to hold your consult.  jenn rulz ok`)
        } else if len(preparedMessage) > 1 && message.Channel == "clan" {
            // buff requests don't count.
            id       := resolvePlayerID(message.Who.Id)
            idNum, _ := strconv.Atoi(id)
            if idNum > 0 {
                // No Frosty thank you
                bot.MaybeSendCarePackage(message.Who.Name)
            }
        }
    }()

    return finalMsg, nil
}


package main
import (
    "regexp"
    "fmt"
    "golang.org/x/net/html"

    "strings"
    "bytes"
    "github.com/Hugmeir/kolgo"
)

// Sigh, discord, why...
// We wrap all messages in ``s to prevent abuse.
// So the only metacharacter we need to quote is '`'.  Even '\' is not a metacharacter.. unless
// it comes at the start of a string, which is not a problem for us because we
// precede all messages with the username.
// Due to purist crap reasons, no positive lookbehind (?<=\\) or lookahead in the default regex
// engine, and no recursive regexes either.
// So just double down on the quotiness:
func EscapeDiscordMetaCharacters(s string) string {
    // `a`b`    => `a``b`,     which displays as [a``b]
    // `a``b``  => `a````b`,   which displays as [a````b] (yep...)
    // `a```b`  => `a``````b`, which displays as [`a``````b`] (...yepp...)
    // basically, once double-backticked, it stops being a metacharacter.
    s = strings.Replace(s, "`", "``", -1)
    if s[0] == '`' {
        s = " " + s
    }
    if s[len(s)-1] == '`' {
        // Sigh... A backtick at the end of the string messes things up, so:
        s = s + " "
    }
    return s
}

var linkMatcher    *regexp.Regexp = regexp.MustCompile(`(?i)<a target=_blank href="([^"]+)"[^>]*><font[^>]+>[^<]+<[^>]+><\\?/a>`)
func FixMangledChatLinks(a string) string {
    s := []byte(a)

    for max := 10; max > 1; max-- {
        loc := linkMatcher.FindSubmatchIndex(s);
        if len(loc) <= 0 {
            // No matches!
            break
        }

        // Grab the url first
        urlRaw := make([]byte, loc[3] - loc[2])
        copy(urlRaw, s[loc[2]:loc[3]])

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

    return string(s)
}

var effectMatcher  *regexp.Regexp = regexp.MustCompile(`(?i)<img src="[^"]+12x12(heart|skull)\.[^"]+"[^>]*>`)
var slashMeMatcher *regexp.Regexp = regexp.MustCompile(`(?i)\A<b><i><a target=mainpane href=[^>]+><font color[^>]+>([^<]+)<\\?/b><\\?/font><\\?/a>(.+)<\\?/i>\z`)
var effectToCmdDefaults  map[string]string = map[string]string{
    // Defaults:
    "heart": `🖤`,
    "skull": `☠`,
    "?": "",
}
func HandleKoLPublicMessage(kol kolgo.KoLRelay, message kolgo.ChatMessage, effectToCmd map[string]string) (string, error) {
    rawMessage     := message.Msg;
    preparedSender  := fmt.Sprintf("**%s**: ", message.Who.Name)
    preparedMessage := html.UnescapeString(rawMessage)

    wrapAround := make(map[string]bool, 3)

    preparedMessage = FixMangledChatLinks(preparedMessage)

    if strings.HasPrefix(preparedMessage, "<") {
        // golden text, chat effects, etc.
        preparedMessage = effectMatcher.ReplaceAllStringFunc(preparedMessage, func(t string) string {
            wrapperType := "?"
            if strings.Contains(t, `heart`) {
                wrapperType = `heart`
            } else if strings.Contains(t, `skull`) {
                wrapperType = `skull`
            }

            c, ok := effectToCmd[wrapperType]
            if !ok {
                c = effectToCmdDefaults[wrapperType]
            }
            wrapAround[c] = true
            return ``
        })

        if meMatch := slashMeMatcher.FindStringSubmatch(preparedMessage); len(meMatch) > 0 {
            // /me foo
            wrapAround["_"] = true
            preparedSender  = fmt.Sprintf("**`%s`**", meMatch[1])
            preparedMessage = " " + meMatch[2] // message WITHOUT the username
        }

        tokens := html.NewTokenizer(strings.NewReader(preparedMessage))
        preparedMessage = ""
        loop:
        for {
            tt := tokens.Next()
            switch tt {
            case html.ErrorToken:
                break loop
            case html.TextToken:
                preparedMessage = preparedMessage + string(tokens.Text())
            }
            // TODO: could grab colors & apply them in markdown
        }
    }

    preparedMessage = EscapeDiscordMetaCharacters(preparedMessage)

    finalMsg := fmt.Sprintf("%s`%s`", preparedSender, preparedMessage)

    for wrap, _ := range wrapAround {
        finalMsg = wrap + finalMsg + wrap
    }

    if message.Channel != "clan" {
        finalMsg = fmt.Sprintf("[%s] %s", message.Channel, finalMsg)
    }

    return finalMsg, nil
}


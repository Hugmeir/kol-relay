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
var brokenLinkRe   *regexp.Regexp = regexp.MustCompile(`(?i)(?:[a-z0-9-]+[a-z09]\.)\s*+(?:c\s*o\s*m|n\s*e\s*t|o\s*r\s*g)\s*/((?:(?:\s[a-z0-9-]+|(?:[a-z0-9-]+ -)*)+/?)+)`)
func FixMangledChatLinks(a string) string {
    c := strings.Replace(a, `https:// `, `https://`, -1)
    c  = strings.Replace(c,  `http:// `, `http://`,  -1)
    s := []byte(a)

    for max := 10; max > 1; max-- {
        loc := linkMatcher.FindSubmatchIndex(s);
        if len(loc) <= 0 {
            // No matches!
            break
        }

        // Grab the "good" url out of the <a> before we shift things around:
        urlRaw     := s[loc[2]:loc[3]]
        url        := []byte(regexp.QuoteMeta(string(urlRaw)))
        url         = bytes.Replace(url, []byte(`/`), []byte(`\s*/\s*`), -1)
        url         = bytes.Replace(url, []byte(`-`), []byte(`\s*-\s*`), -1)
        urlRe, err := regexp.Compile(string(url))
        if err == nil {
            // If it failed to compile, meh, just ignore it; otherwise,
            // use the regex we just created to replace the broken urls
            s = urlRe.ReplaceAll(s, urlRaw)
        } else {
            fmt.Println("Regexp failed to compile with ", err)
        }

        // Now get rid of the whole <a> eyesore:
        // The following is the go way of doing this
        // s = s[:loc[0]] + s[loc[1]:]
        // One day...
        s = s[:loc[0] + copy(s[loc[0]:], s[loc[1]+1:])]
    }

    for max := 10; max > 1; max-- {
        loc := brokenLinkRe.FindSubmatchIndex(s)
        if len(loc) <= 0 {
            // No matches!
            break
        }

        fixedUrl := bytes.Replace(s[loc[2]:loc[3]], []byte(` `), []byte(``), -1)
        //fmt.Println(fixedUrl)
        //s = s[:loc[2] + copy(s[loc[2]:], s[loc[3]+1:])]
        // ugh I can't get this to work
        s = []byte(string(s[:loc[2]]) + string(fixedUrl) + string(s[loc[3]:]))
    }

    return string(s)
}

var effectMatcher  *regexp.Regexp = regexp.MustCompile(`(?i)<img src="[^"]+12x12(heart|skull)\.[^"]+"[^>]*>`)
var slashMeMatcher *regexp.Regexp = regexp.MustCompile(`(?i)\A<b><i><a target=mainpane href=[^>]+><font color[^>]+>([^<]+)<\\?/b><\\?/font><\\?/a>(.+)<\\?/i>\z`)
var effectToCmd    map[string]string = map[string]string{
    "heart": `<:chatheart:493814910111842306>`,
    "skull": `<:chatskull:493815533490143243>`,
}
func HandleKoLPublicMessage(kol kolgo.KoLRelay, message kolgo.ChatMessage) (string, error) {
    rawMessage     := message.Msg;
    preparedSender  := fmt.Sprintf("**%s**: ", message.Who.Name)
    preparedMessage := html.UnescapeString(rawMessage)

    wrapAround := make(map[string]bool, 3)

    preparedMessage = FixMangledChatLinks(preparedMessage)

    if strings.HasPrefix(preparedMessage, "<") {
        // golden text, chat effects, etc.
        if meMatch := slashMeMatcher.FindStringSubmatch(preparedMessage); len(meMatch) > 0 {
            // /me foo
            wrapAround["_"] = true
            preparedSender  = fmt.Sprintf("**`%s`**", meMatch[1])
            preparedMessage = " " + meMatch[2]
        } else {
            preparedMessage = effectMatcher.ReplaceAllStringFunc(preparedMessage, func(t string) string {
                if strings.Contains(t, `heart`) {
                    wrapAround[effectToCmd[`heart`]] = true
                } else {
                    wrapAround[effectToCmd[`skull`]] = true
                }
                return ``
            })

            // TODO: Why are we doing this twice??
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
    }

    preparedMessage = EscapeDiscordMetaCharacters(preparedMessage)

    finalMsg := fmt.Sprintf("%s`%s`", preparedSender, preparedMessage)

    for wrap, _ := range wrapAround {
        finalMsg = wrap + finalMsg + wrap
    }

    if message.Channel != "clan" {
        finalMsg = fmt.Sprintf("[%s] %s", message.Channel, finalMsg)
    }

//    finalMsg = "<:chatheart:493814910111842306>" + finalMsg + "<:chatskull:493815533490143243>"

    return finalMsg, nil
}


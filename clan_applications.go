package main

import (
    "regexp"
    "bytes"
    "golang.org/x/net/html"
)

type ClanApplication struct {
    PlayerName string
    PlayerID   string
    RequestID  string
}

var playerIDMatcher = regexp.MustCompile(`showplayer\.php\?who=([0-9]+)`)
func DecodeClanApplications(body []byte) []ClanApplication {
    tokens := html.NewTokenizer(bytes.NewReader(body))
    applications := []ClanApplication{}
    loop:
    for {
        tt := tokens.Next()
        switch tt {
            case html.ErrorToken:
                break loop
            case html.StartTagToken:
                t := tokens.Token()
                if t.Data != "table" {
                    continue
                }
                tt = tokens.Next()
                if tt == html.TextToken {
                    tt = tokens.Next()
                }

                if tt != html.StartTagToken {
                    continue
                }
                t = tokens.Token()

                if t.Data != "form" {
                    continue
                }

                // finally.  Start parsing the entries.
                for tokens.Token().Data != "table" {
                    tt := tokens.Next()
                    if tt == html.StartTagToken {
                        t := tokens.Token()
                        if t.Data == "td" {
                            tokens.Next()
                            innert  := tokens.Token()
                            // If we have an <a>, we have hit gold
                            if innert.Data != "a" {
                                continue
                            }

                            raw := tokens.Raw()
                            matches := playerIDMatcher.FindSubmatch(raw)
                            if len(matches) < 2 {
                                continue
                            }

                            playerID := string(matches[1])
                            tokens.Next()
                            playerName := string(tokens.Text())
                            current := ClanApplication{
                                PlayerName: playerName,
                                PlayerID:   playerID,
                                RequestID:  "request" + playerID,
                            }
                            applications = append(applications, current)
                        }
                    } else if tt == html.ErrorToken {
                        break
                    }
                }
        }
    }
    return applications
}

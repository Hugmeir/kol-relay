package main

import (
    "regexp"
    "strconv"
    "bytes"
    "golang.org/x/net/html"
)

type ClanMember struct {
    Name        string
    ID          string
    Rank        string
    Title       string
    RequestID   string
}

/*
Jump to page: <b>[1]</b> <a href="clan_members.php?begin=2">[2]</a> <a h    ref="clan_members.php?begin=3">[3]</a> <a href="clan_members.php?begin=4">[4]</a> <a href="clan_members.php?begin=5">[5]</a> <a href="clan_members.php?begin=6">[6]</a> </td>
*/
// TODO; only works on page1:
var memberPages = regexp.MustCompile(`(?i)Jump to page:\s*<b>\Q[1]\E</b>\s*(?:\s*<a[^>]+>[^<]+</a>\s*)*\s*<a href=['"]clan_members\.php\?begin=(\d+)['"]>\[\d+\]</a>\s*</td>`)
func DecodeTotalMemberPages(b []byte) int {
    m := memberPages.FindSubmatch(b)
    if len(m) < 1 {
        return 0
    }
    n := string(m[1])
    i, err := strconv.Atoi(n)
    if err != nil {
        return 0
    }
    return i
}

var playerIDName = regexp.MustCompile(`showplayer\.php\?who=([0-9]+)['"][^>]*>`)
func DecodeClanMembers(b []byte) []ClanMember {
    clannies := make([]ClanMember, 0, 100)
    tokens := html.NewTokenizer(bytes.NewReader(b))
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
                        if t.Data != "td" {
                            continue
                        }
                        tokens.Next()
                        tokens.Next()
                        innert  := tokens.Token()
                        // If we have an <a>, we have hit gold
                        if innert.Data != "a" {
                            continue
                        }

                        raw := tokens.Raw()
                        matches := playerIDName.FindSubmatch(raw)
                        if len(matches) < 2 {
                            continue
                        }

                        playerID   := string(matches[1])

                        tokens.Next()
                        playerName := string(tokens.Text())

                        // sigh...
                        tokens.Next()
                        tokens.Next()
                        tokens.Next()
                        tokens.Next()
                        tokens.Next()
                        playerRank := string(tokens.Text())

                        tokens.Next()
                        tokens.Next()
                        tokens.Next()
                        playerTitle := string(tokens.Text())
                        current := ClanMember{
                            Name:      playerName,
                            ID:        playerID,
                            Rank:      playerRank,
                            Title:     playerTitle,
                            RequestID: "request" + playerID,
                        }
                        clannies = append(clannies, current)
                    } else if tt == html.ErrorToken {
                        break
                    }
                }
        }
    }
    return clannies
}


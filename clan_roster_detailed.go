package main

import (
    "regexp"
    "fmt"
)

type ClanRosterDetailed struct {
    ID    string
    Name  string
    Rank  string
}

// Screw using the html tokenizer forever, seriously
var re = `(?i)
    <tr>                                                                     \s*
        <td \s+ class=['"]?small['"]?>                                       \s*
            <a [^>]+ href=['"]showplayer\.php\?who=(?P<player_id>\d+)['"] [^>]*>   \s*
                <b>(?P<player_name>[^<]+)</b>                                \s*
                (?:&nbsp;)*                                                  \s*
            </a>                                                             \s*
        </td>                                                                \s*
        (?: <td \s+ class=['"]?small['"]?>[^<]+</td> \s* ){8}                \s*
        <td \s+ class=['"]?small['"]?>(?P<rank>[^<]+)</td>                   \s*
        <td \s+ class=['"]?small['"]?>[^<]+</td>                             \s*
    </tr>
`
var wsMatcher        = regexp.MustCompile(`\s+`)
var detailedRoster   = regexp.MustCompile(wsMatcher.ReplaceAllString(re, ``))
func DecodeDetailedRoster(b []byte) []ClanRosterDetailed {
    roster  := make([]ClanRosterDetailed, 0, 600)
    matches := detailedRoster.FindAllStringSubmatch(string(b), -1)
    fmt.Println(detailedRoster)
    if len(matches) == 0 {
        return roster
    }

    for _, m := range matches {
        roster = append(roster, ClanRosterDetailed{
            ID:    m[1],
            Name:  m[2],
            Rank:  m[3],
        })
    }
    return roster
}


package main

import (
    "regexp"
)

type ClanWhitelistEntry struct {
    ID    string
    Name  string
    Rank  string
    Title string
}

// Screw using the html tokenizer forever, seriously
var wlRe = `(?i)
    <tr>                                                                     \s*
        <td>                                                                 \s*
            <input [^>]+>                                                    \s*
            <a [^>]+ href=['"]showplayer\.php\?who=(?P<player_id>\d+)['"] [^>]*>   \s*
                <b>(?P<player_name>[^<]+)</b>                                \s*
                \(#\d+\)                                                     \s*
            </a>                                                             \s*
        </td>                                                                \s*
        <td>(?P<rank>[^<]+)</td>                                             \s*
        <td>(?P<title>[^<]+)</td>                                            \s*
        (?:
              [^<]+
            | <[^/]
            | </[^t]
            | </t[^r]
        )*
    </tr>
`
var whitelistMatcher = regexp.MustCompile(wsMatcher.ReplaceAllString(wlRe, ``))
func DecodeClanWhitelist(b []byte) []ClanWhitelistEntry {
    wl := make([]ClanWhitelistEntry, 0, 600)
    matches := whitelistMatcher.FindAllStringSubmatch(string(b), -1)
    if len(matches) == 0 {
        return wl
    }

    for _, m := range matches {
        wl = append(wl, ClanWhitelistEntry{
            ID:    m[1],
            Name:  m[2],
            Rank:  m[3],
            Title: m[4],
        })
    }
    return wl
}


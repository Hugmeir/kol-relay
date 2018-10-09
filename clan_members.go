package main

import (
    "regexp"
    "strconv"
)

type ClanMember struct {
    Name        string
    ID          string
    Rank        string
    Title       string
    Inactive    bool
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

// Screw using the html tokenizer forever, seriously
var memberRe = `(?i)
    <tr>                                                                     \s*
        <td>                                                                 \s*
            <input [^>]+>                                                    \s*
            <a [^>]+ href=['"]showplayer\.php\?who=(?P<player_id>\d+)['"] [^>]*>   \s*
                (?P<player_name>[^<]+)                                       \s*
            </a>                                                             \s*
            (<font [^>]+>\s*<b>\s*\Q(inactive)\E</b>\s*</font>\s*)*
            (?:&nbsp;)*                                                      \s*
        </td>                                                                \s*
        <td>
            (?P<rank>
                  [^<]+
                  | <select [^>]+> \s* (?:<option[^>]+>[^<]+</option>\s*)+</select>
            ) \s*
        </td>                                            \s*
        <td>                                             \s*
            <input [^>]+ value="(?P<title>[^"]*)"[^>]*>  \s*
        </td>                                            \s*
    </tr>
`
var memberMatcher = regexp.MustCompile(wsMatcher.ReplaceAllString(memberRe, ``))
func DecodeClanMembers(b []byte) []ClanMember {
    clannies := make([]ClanMember, 0, 100)
    matches  := memberMatcher.FindAllStringSubmatch(string(b), -1)
    if len(matches) == 0 {
        return clannies
    }

    for _, m := range matches {
        id := m[1]
        inactive := false
        if m[3] != "" {
            inactive = true
        }
        clannies = append(clannies, ClanMember{
            ID:        m[1],
            Name:      m[2],
            Inactive:  inactive,
            Rank:      m[4],
            Title:     m[5],
            RequestID: "request" + id,
        })
    }
    return clannies
}


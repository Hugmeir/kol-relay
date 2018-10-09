package main

import (
    "regexp"
    "strings"
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
            (
                  <input [^>]+ value="[^"]*"[^>]*>
                | [^<]*
            ) \s*
        </td>                                            \s*
    </tr>
`
var memberMatcher = regexp.MustCompile(wsMatcher.ReplaceAllString(memberRe, ``))
var selectedRank  = regexp.MustCompile(`(?i)<option.+selected[^>]*>([^<]+)\s\([^)]+\)</option>`)
var titleMatcher  = regexp.MustCompile(`(?i)<input[^>]+value="([^"]*")`)
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
        rank := m[4]
        if strings.Contains(rank, `<select`) {
            m2 := selectedRank.FindStringSubmatch(rank)
            rank = m2[1]
        }
        title := m[5]
        if strings.Contains(title, `<input`) {
            m2 := titleMatcher.FindStringSubmatch(title)
            title = m2[1]
        }
        clannies = append(clannies, ClanMember{
            ID:        id,
            Name:      m[2],
            Inactive:  inactive,
            Rank:      rank,
            Title:     title,
            RequestID: "request" + id,
        })
    }
    return clannies
}


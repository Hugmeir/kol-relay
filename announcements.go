package main

import (
    "bytes"
    "golang.org/x/net/html"
)

type ClanAnnouncement struct {
    Author       string
    Date         string
    Announcement string
}

func ClanAnnouncements(clanHall []byte) []ClanAnnouncement {
    tokens := html.NewTokenizer(bytes.NewReader(clanHall))
    inAnnouncements := 0
    announcements := []ClanAnnouncement{}
    date := ""
    author := ""

    loop:
    for {
        tt := tokens.Next()
        switch tt {
            case html.ErrorToken:
                break loop
            case html.StartTagToken:
                t := tokens.Token()
                if t.Data == "center" {
                    tt = tokens.Next()
                    if tt != html.TextToken {
                        continue
                    }
                    txt := tokens.Text()
                    if bytes.Equal(txt, []byte("Recent Announcements")){
                        inAnnouncements = 1
                    }
                } else if t.Data == "b" && inAnnouncements == 1 {
                    tt = tokens.Next()
                    if tt != html.TextToken {
                        continue
                    }

                    date = string(tokens.Text())

                    tt = tokens.Next()
                    tt = tokens.Next()
                    if tt != html.TextToken {
                        continue
                    }
                    author = string(tokens.Text())
                } else if t.Data == "blockquote" && inAnnouncements == 1 {
                    announcement := ""
                    inner:
                    for {
                        tt = tokens.Next();
                        switch tt {
                            case html.TextToken:
                                announcement = announcement + string(tokens.Text())
                            case html.SelfClosingTagToken:
                                continue
                            default:
                                break inner
                        }
                    }
                    if announcement == "" {
                        continue
                    }
                    announcements = append(announcements, ClanAnnouncement{
                        Announcement: announcement,
                        Date:         date,
                        Author:       author,
                    })
                    date = ""
                    author = ""
                }
            case html.EndTagToken:
                if inAnnouncements == 0 {
                    continue
                }
        }
    }

    return announcements
}

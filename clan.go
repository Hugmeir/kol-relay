package main

import (
    "fmt"
    "strings"
    "bytes"
    "time"
    "sync"
    "github.com/Hugmeir/kolgo"
    "database/sql"
)

type ToilBot struct {
    KoL       kolgo.KoLRelay
    BlackList sync.Map
    Stop      bool
}

func NewToilBot(username string, password string, db *sql.DB) *ToilBot {
    kol := kolgo.NewKoL(username, nil)

    err := kol.LogIn(password)
    if err != nil {
        panic(err)
    }

    bot := &ToilBot{
        KoL: kol,
        Stop: false,
    }

    rows, err := db.Query("SELECT player_name FROM kol_blacklist")
    if err != nil {
        bot.Stop = true
        return bot
    }
    defer rows.Close()
    for rows.Next() {
        var playerName  string
        err = rows.Scan(&playerName)
        if err != nil {
            fmt.Println(err)
            continue
        }
        bot.BlackList.Store(playerName, true)
    }

    return bot
}

const FCA_FreshFish = "9"
const FCA_WELCOME = `Hi, and welcome to FCA!

Come hang out in chat (type '/c clan' in the chat pane) to get a title and get ranked up to Pleasure Seeker.

Once you are ranked up, you'll be able to access the clan stash and clan dungeon, and you'll automatically get a whitelist too!  Please read the rules for dungeon use in the clan forum, or ask in chat.

Feel free to join the clan Discord: https://discord.gg/CmSfAgq`

const FCA_AnnounceKoLFmt = `Player --> %s (#%s) was just accepted to the clan.`

func (toil *ToilBot)BlacklistedName(n string) bool {
    n = strings.ToLower(n)
    if _, ok := toil.BlackList.Load(n); ok {
        return true
    }

    n = strings.Replace(n, ` `, `_`, -1)
    if _, ok := toil.BlackList.Load(n); ok {
        return true
    }

    n = strings.Replace(n, `_`, ` `, -1)
    if _, ok := toil.BlackList.Load(n); ok {
        return true
    }

    return false
}

func (bot *Chatbot)PollClanApplications(announceChannel string, toilConf *toilBotConf, db *sql.DB) {
    toilbot := NewToilBot(toilConf.Username, toilConf.Password, db)
    ticker := time.NewTicker(5 * time.Minute)
    defer ticker.Stop()
    defer func() { fmt.Println("No longer polling for new applications") }()
    for {
        if toilbot.Stop {
            return
        }
        select {
        case <-ticker.C:
            body, err := bot.KoL.ClanApplications()
            if err != nil {
                fatalError := bot.KoL.HandleKoLException(err, toilConf.Password)
                if fatalError != nil {
                    fmt.Println("Unable to poll for new applications: ", err)
                    continue
                }
            }
            applications := DecodeClanApplications(body)
            if len(applications) < 1 {
                continue
            }

            // Okay, we got applications.  So far we've been using relaybot to
            // do the polling, but that was just to save resources.  To do
            // proper clan management, we need to use the toilbot:
            kol := toilbot.KoL
            for _, app := range applications {
                if toilbot.BlacklistedName(app.PlayerName) {
                    fmt.Printf("REJECTING application from blacklisted user %s\n", app.PlayerName)
                    _, err := kol.ClanProcessApplication(app.RequestID, false)
                    if err != nil {
                        if err := kol.HandleKoLException(err, toilConf.Password); err != nil {
                            fmt.Println("Failed to reject an application: ", err)
                        }
                    }
                    continue
                }

                _, month, day := time.Now().Date()
                title := fmt.Sprintf("%02d/%02d awaiting Naming Day", int(month), day)
                announcement := fmt.Sprintf(FCA_AnnounceKoLFmt, app.PlayerName, app.PlayerID)

                body, err := kol.ClanProcessApplication(app.RequestID, true)
                if err != nil {
                    if err := kol.HandleKoLException(err, toilConf.Password); err != nil {
                        fmt.Println("Failed to accept an application: ", err)
                        continue
                    }
                }
                if bytes.Contains(body, []byte(`You cannot accept new members into the clan.`)) {
                    toilbot.Stop = true
                    return
                }

                body, err = kol.ClanModifyMember("1", app.PlayerID, FCA_FreshFish, title)
                if err != nil {
                    fmt.Println("Failed to process an application: ", err)
                    continue
                }

                kol.SendKMail(app.PlayerName, FCA_WELCOME)
                // Notice how Relay sends the message here, NOT Toil.  Toil does not
                // have access to chat.
                bot.KoL.SendMessage("/clan", announcement)
                bot.Discord.ChannelMessageSend(announceChannel, announcement)
            }
    } }
}


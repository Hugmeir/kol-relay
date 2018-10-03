package main
import (
    "regexp"
    "fmt"
    "github.com/Hugmeir/kolgo"
)

type kolEventHandler struct {
    re *regexp.Regexp
    cb func(kolgo.KoLRelay, kolgo.ChatMessage, []string) (string, error)
}
var kolEventHandlers = []kolEventHandler{
    kolEventHandler {
        /*jawbruised*/
        regexp.MustCompile(`(?i)<a href='showplayer\.php\?who=([0-9]+)' [^>]+>([^<]+)<\/a> has hit you in the jaw with a piece of candy`),
        func (kol kolgo.KoLRelay, message kolgo.ChatMessage, matches []string) (string, error) {
            fmt.Printf("Jawbruised by %s (%s), raw message: %s", matches[1], matches[2], message.Msg)
            senderId := matches[1]
            kol.SendMessage("/msg " + senderId, "C'mon, don't be a dick.")

            cleared, _ := ClearJawBruiser(kol)
            toDiscord := fmt.Sprintf("%s (#%s) jawbruised the bot.", matches[2], matches[1])
            if ! cleared {
                partialStfu = true
                toDiscord   = toDiscord + " And it could not be uneffected, so the bot will stop relaying messages to KoL."
            }
            return toDiscord, nil
        },
    },
    kolEventHandler {
        /*snowball*/
        /*
        {"msgs":[{"type":"event","msg":"That rotten jerk <a href='showplayer.php?who=3061055' target=mainpane class=nounder style='color: green'>Hugmeir<\/a> plastered you in the face with a snowball! Grr! Also, Brr!<!--refresh-->","link":false,"time":"1537390984"}],"last":"1468370925","delay":3000}
        */
        regexp.MustCompile(`(?i)<a href='showplayer\.php\?who=([0-9]+)' [^>]+>([^<]+)<\/a> has hit you in the jaw with a piece of candy`),
        func (kol kolgo.KoLRelay, message kolgo.ChatMessage, matches []string) (string, error) {
            fmt.Printf("Hit by a snowball from %s (%s), raw message: %s", matches[1], matches[2], message.Msg)
            senderId := matches[1]
            kol.SendMessage("/msg " + senderId, "How about you don't?  That'll just be irritating for people reading chat.")

            cleared, _ := ClearSnowball(kol)
            toDiscord := fmt.Sprintf("%s (#%s) threw a snowball at the bot.", matches[2], matches[1])
            if ! cleared {
                toDiscord = toDiscord + " And it could not be uneffected, so the relayed messages will get effects."
            }
            return toDiscord, nil
        },
    },
    kolEventHandler {
        /*announcement*/
        /*{"msgs":[{"type":"event","msg":"A new announcement has been posted in your Clan Hall.","link":"clan_hall.php","time":"1538421708"}],"last":"1468905074","delay":3000}*/
        regexp.MustCompile(`(?i)\AA new announcement has been posted in your Clan Hall\.\z`),
        func (kol kolgo.KoLRelay, message kolgo.ChatMessage, matches []string) (string, error) {
            clanHall, err := kol.ClanHall()
            if err != nil {
                fmt.Println(err)
                return message.Msg, nil
            }
            announcements := ClanAnnouncements(clanHall)
            if len(announcements) < 1 {
                return message.Msg, nil
            }

            latest := announcements[0]
            toDiscord := fmt.Sprintf("%s\n```\n%s\n\n%s\n```", message.Msg, latest.Author, EscapeDiscordMetaCharacters(latest.Announcement))
            // Mina asked that announcements also get reflected in KoL chat, so:
            go func() {
                msg := "Announcement: " + latest.Announcement
                if len(msg) < 200 {
                    kol.SendMessage("/clan", msg)
                }
                // Okay, so... There was an announcement, but it was too long.
                // Drop it for now
            }()
            return toDiscord, nil
        },
    },
}
func HandleKoLEvent(kol kolgo.KoLRelay, message kolgo.ChatMessage) (string, error) {
    for _, handler := range kolEventHandlers {
        matches := handler.re.FindStringSubmatch(message.Msg)
        if len(matches) > 0 {
            return handler.cb(kol, message, matches)
        }
    }

    return "", nil
}



package main
import (
    "time"
    "bytes"
    "regexp"
    "fmt"
    "github.com/Hugmeir/kolgo"
)

// TODO: get from config
const (
    FCA_RESPONSE1 = `fries`
    FCA_RESPONSE2 = `robin`
    FCA_RESPONSE3 = `thin`
)

var newMessageEventRe = regexp.MustCompile(`(?i)\ANew message received from <a[^>]+\bwho=(\d+)["'][^>]*>`)

type kolEventHandler struct {
    re *regexp.Regexp
    cb func(*Chatbot, kolgo.ChatMessage, []string) (string, error)
}
var kolEventHandlers = []kolEventHandler{
    kolEventHandler {
        /*jawbruised*/
        regexp.MustCompile(`(?i)<a href='showplayer\.php\?who=([0-9]+)' [^>]+>([^<]+)<\/a> has hit you in the jaw with a piece of candy`),
        func (bot *Chatbot, message kolgo.ChatMessage, matches []string) (string, error) {
            fmt.Printf("Jawbruised by %s (%s), raw message: %s", matches[1], matches[2], message.Msg)
            senderId := matches[1]
            bot.KoL.SendMessage("/msg " + senderId, "C'mon, don't be a dick.")

            e := &Effect{
                ID:   bruisedJaw,
                Name: "Bruised Jaw",
            }

            cleared, _ := bot.Uneffect(e)
            toDiscord := fmt.Sprintf("%s (#%s) jawbruised the bot.", matches[2], matches[1])
            if ! cleared {
                bot.PartialStfu = true
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
        regexp.MustCompile(`(?i)That rotten jerk <a href='showplayer\.php\?who=([0-9]+)' [^>]+>([^<]+)<\/a> plastered you`),
        func (bot *Chatbot, message kolgo.ChatMessage, matches []string) (string, error) {
            fmt.Printf("Hit by a snowball from %s (%s), raw message: %s", matches[1], matches[2], message.Msg)
            senderId := matches[1]
            bot.KoL.SendMessage("/msg " + senderId, "How about you don't?  That'll just be irritating for people reading chat.")

            e := &Effect{
                ID:   snowBall,
                Name: "B-b-brr!",
            }

            cleared, _ := bot.Uneffect(e)
            toDiscord := fmt.Sprintf("%s (#%s) threw a snowball at the bot.", matches[2], matches[1])
            if ! cleared {
                toDiscord = toDiscord + " And it could not be uneffected, so the relayed messages will get effects."
            }
            return toDiscord, nil
        },
    },
    kolEventHandler {
        /*safari*/
        /* {"msgs":[{"type":"event","msg":"<a class=nounder target=mainpane href=showplayer.php?who=1740784><b>Ganomex<\/b><\/a> has blessed you with the ability to experience a safari adventure. (15 Adventures)","link":false,"time":"1541714573"}],"last":"1470542290","delay":3000} */
        regexp.MustCompile(`(?i)<a[^>]+href=["']?showplayer\.php\?who=([0-9]+)["']?[^>]*>(?:<b>)?([^<]+)(?:<\\?/b>)?<\\?/a> has blessed you with the ability to experience a safari`),
        func (bot *Chatbot, message kolgo.ChatMessage, matches []string) (string, error) {
            fmt.Printf("Safari'd by %s (%s), raw message: %s", matches[1], matches[2], message.Msg)
            senderId := matches[1]
            bot.KoL.SendMessage("/msg " + senderId, "Remember -- keep your abuse precise & directed.  By hitting the relay you are annoying too many people at once.")

            e := &Effect{
                ID:   onSafari,
                Name: "On Safari",
            }

            time.Sleep(1 * time.Second)
            cleared, _ := bot.Uneffect(e)
            toDiscord := fmt.Sprintf("%s (#%s) made the bot go on an unwanted safari adventure.", matches[2], matches[1])
            if ! cleared {
                toDiscord = toDiscord + " And it could not be uneffected, so the relayed messages will get effects."
            }
            return toDiscord, nil
        },
    },
    kolEventHandler {
        /*demotivator*/
        /* sent you a really unmotivating card */
        regexp.MustCompile(`(?i)<a href='showplayer\.php\?who=([0-9]+)' [^>]+>([^<]+)<\/a> sent you a really unmotivating card`),
        func (bot *Chatbot, message kolgo.ChatMessage, matches []string) (string, error) {
            senderId := matches[1]
            bot.KoL.SendMessage("/msg " + senderId, "Meh...")

            e := &Effect{
                ID:   unmotivated,
                Name: "Unmotivated",
            }

            cleared, _ := bot.Uneffect(e)
            toDiscord := fmt.Sprintf("%s (#%s) demotivated the bot.", matches[2], matches[1])
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
        func (bot *Chatbot, message kolgo.ChatMessage, matches []string) (string, error) {
            kol := bot.KoL
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
    kolEventHandler {
        /* new kmail */
        /*{"msgs":[{"type":"event","msg":"New message received from <a target=mainpane href='showplayer.php?who=2685812'><font color=green>Chick Norris<\/font><\/a>.","link":"messages.php","time":"1539345944"}],"last":"1469379751","delay":3000}*/
        newMessageEventRe,
        func (bot *Chatbot, message kolgo.ChatMessage, matches []string) (string, error) {
            kol := bot.KoL
            args := map[string]string{}
            if since, ok := bot.SinceAPI.Load(`events`); ok {
                args["since"] = since.(string)
            } else {
                args["since"] = time.Now().Add(time.Duration(-20) * time.Second).Format(time.RFC3339)
            }
            eventsRaw, err := kol.APIRequest(`events`, &args)
            if err != nil {
                fmt.Println("Failed to get recent events: ", err)
                return "", err
            }

            bot.SinceAPI.Store(`events`, time.Now().Format(time.RFC3339))

            events, _ := kolgo.DecodeAPIEvent(eventsRaw)
            toDiscord := ""
            for _, e := range events {
                if !newMessageEventRe.MatchString(e.Message) {
                    continue
                }
                payload  := e.Payload
                playerID := payload[`from`]
                kmailID  := payload[`id`]
                out, err := bot.HandleKMail(playerID, kmailID)
                if err == nil {
                    toDiscord = toDiscord + out
                }
            }
            return toDiscord, nil
        },
    },
    kolEventHandler {
        /* consult */
        /*You have been invited to <a style='color: green' target='mainpane' href='clan_viplounge.php?preaction=testlove&testlove=3061055'>consult Madame Zatara about your relationship<\/a> with Hugmeir.","link":false,"time":"1539627284"}],"last":"1469529316","delay":3000}*/
        regexp.MustCompile(`(?i)You have been invited to <a[^>]+href=["']clan_viplounge\.php\?preaction=testlove&testlove=([0-9]+)['"]>consult Madame Zatara`),
        func (bot *Chatbot, message kolgo.ChatMessage, matches []string) (string, error) {
            sucker := matches[1]
            kol    := bot.KoL
            if _, ok := bot.HoldConsultsFor.Load(sucker); ok {
                bot.HoldConsultsFor.Store(sucker, FORTUNE_REAL_CONSULT)
                kol.SendMessage("/msg " + sucker, "Will hold that consult until you send a PM saying 'release'")
                return "", nil
            }

            body, err := bot.RespondToConsult(sucker)

            if err != nil {
                fmt.Println("Failed to respond to consult: ", err)
                kol.SendMessage("/msg " + sucker, "Sorry, had some error when respoding to your consult.  Will try again in ~30m")
            } else if !ConsultResponseWasSuccessful(body) {
                kol.SendMessage("/msg " + sucker, "Sorry, had some error when respoding to your consult.  Will try again in ~30m")
            }

            return "", nil
        },
    },
}
func (bot *Chatbot)HandleKoLEvent(message kolgo.ChatMessage) (string, error) {
    for _, handler := range kolEventHandlers {
        matches := handler.re.FindStringSubmatch(message.Msg)
        if len(matches) > 0 {
            return handler.cb(bot, message, matches)
        }
    }

    return "", nil
}


func (bot *Chatbot) RespondToConsult(playerID string) ([]byte, error) {
    kol     := bot.KoL

    bot.HoldConsultsFor.Delete(playerID)

    return kol.ClanResponseLoveTest(
        playerID,
        FCA_RESPONSE1,
        FCA_RESPONSE2,
        FCA_RESPONSE3,
    )
}

func ConsultResponseWasSuccessful(b []byte) bool {
    if bytes.Contains(b, []byte(`Thanks!  We'll calculate your results and send them to`)) {
        return true
    }
    return false
}


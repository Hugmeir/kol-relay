package main

import (
    "fmt"
    "time"
    "regexp"
    "strings"
    "strconv"
    "math/rand"
    "github.com/Hugmeir/kolgo"
)

type privateMsgHandler struct {
    re *regexp.Regexp
    cb func(*Chatbot, kolgo.ChatMessage, []string) (string, error)
}

func resolvePlayerID(p interface {}) string {
    var id string
    switch p.(type) {
        case string:
            id, _ = p.(string)
        case float64:
            f, _ := p.(float64)
            id = strconv.Itoa(int(f))
    }

    return id
}

var privateMsgHandlers = []privateMsgHandler{
    privateMsgHandler{
        /* user verification */
        regexp.MustCompile(`(?i)^\s*verify(?:\s* me)?!?`),
        HandleKoLVerificationRequest,
    },
    privateMsgHandler{
        /* opt out of getting daily packages*/
        regexp.MustCompile(`(?i)\A\QI do not want to have fun.\E\z`),
        func (bot *Chatbot, message kolgo.ChatMessage, matches []string) (string, error) {
            sucker := strings.ToLower(message.Who.Name)
            bot.OptOutOfDailyPackages(sucker)
            bot.KoL.SendMessage("/msg " + sucker, "This is an acknowledgement that we too consider you boring, and so you will not get any more packages.  Say 'girls, they just want to have fun.' to start getting packages again.")
            return "", nil
        },
    },
    privateMsgHandler{
        /* opt out of getting daily packages*/
        regexp.MustCompile(`(?i)\Agirls,? they just want to have fun\.?\z`),
        func (bot *Chatbot, message kolgo.ChatMessage, matches []string) (string, error) {
            sucker := strings.ToLower(message.Who.Name)
            bot.OptOutOfOptingOutOfDailyPackages(sucker)
            bot.KoL.SendMessage("/msg " + sucker, "Packages as far as the eye can see!")
            return "", nil
        },
    },
    privateMsgHandler{
        /* start holding consults */
        regexp.MustCompile(`(?i)^\s*hold\b`),
        func (bot *Chatbot, message kolgo.ChatMessage, matches []string) (string, error) {
            id := resolvePlayerID(message.Who.Id)
            if _, ok := bot.HoldConsultsFor.Load(id); !ok {
                // Don't override a "real" hold with a virtual one
                // We get here if this happens:
                // "hold"
                // send consult
                // "hold"
                bot.HoldConsultsFor.Store(id, FORTUNE_VIRTUAL_CONSULT)
            }
            bot.KoL.SendMessage("/msg " + id, "Will hold any future consult until you say 'release'")
            return "", nil
        },
    },
    privateMsgHandler{
        /* release consult */
        regexp.MustCompile(`(?i)^\s*release\b`),
        func (bot *Chatbot, message kolgo.ChatMessage, matches []string) (string, error) {
            id := resolvePlayerID(message.Who.Id)
            v, ok := bot.HoldConsultsFor.Load(id)
            if !ok {
                bot.KoL.SendMessage("/msg " + id, "Not holding any consults for you")
                return "", nil
            }

            if v.(string) != FORTUNE_REAL_CONSULT {
                bot.KoL.SendMessage("/msg " + id, "You told me to release a consult, but as far as I can tell I am not holding one.  If this is an error, you will get your consult answered in ~30m")
                bot.HoldConsultsFor.Delete(id)
                return "", nil
            }

            b, err := bot.RespondToConsult(id)
            if err != nil || !ConsultResponseWasSuccessful(b) {
                bot.KoL.SendMessage("/msg " + id, "Sorry, could not respond to your consult.  Will get retried in ~30m")
            }
            return "", nil
        },
    },
}

func HandleKoLVerificationRequest(bot *Chatbot, message kolgo.ChatMessage, matches []string) (string, error) {
    senderId := bot.KoL.SenderIdFromMessage(message)

    _, ok := bot.VerificationPending.Load("User:" + message.Who.Name);
    if ok {
        bot.KoL.SendMessage("/msg " + senderId, "Already sent you a code, you must wait 5 minutes to generate a new one")
        return "", nil
    }

    verificationCode := fmt.Sprintf("%15d", rand.Uint64())
    bot.VerificationPending.Store("Code:" + verificationCode, message.Who.Name)
    bot.VerificationPending.Store("User:" + message.Who.Name, verificationCode)

    bot.KoL.SendMessage("/msg " + senderId, "In Discord, send me a private message saying \"Verify me: " + verificationCode + "\", without the quotes.  This will expire in 5 minutes")

    go func() {
        time.Sleep(5 * time.Minute)
        bot.VerificationPending.Delete("Code:" + verificationCode)
        bot.VerificationPending.Delete("User:" + message.Who.Name)
    }()

    return "", nil
}

func (bot *Chatbot)HandleKoLDM(message kolgo.ChatMessage) (string, error) {
    for _, handler := range privateMsgHandlers {
        m := handler.re.FindStringSubmatch(message.Msg)
        if len(m) > 0 {
            return handler.cb(bot, message, m)
        }
    }

    return "", nil
}


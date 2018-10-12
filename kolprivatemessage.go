package main

import (
    "fmt"
    "time"
    "regexp"
    "math/rand"
    "github.com/Hugmeir/kolgo"
)

type privateMsgHandler struct {
    re *regexp.Regexp
    cb func(*Chatbot, kolgo.ChatMessage, []string) (string, error)
}

var privateMsgHandlers = []privateMsgHandler{
    privateMsgHandler{
        /* user verification */
        regexp.MustCompile(`(?i)^\s*verify(?:\s* me)?!?`),
        HandleKoLVerificationRequest,
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


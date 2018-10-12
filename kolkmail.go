package main

import (
    "fmt"
    "bytes"
    "regexp"
    "github.com/Hugmeir/kolgo"
)

// TODO: should be exposed by kolgo
var giftboxen = map[string]bool{
    "1167": true,
    "1168": true,
    "1169": true,
    "1170": true,
    "1171": true,
    "1172": true,
}

var useOnSender = map[string][]byte{
    "time's arrow":  []byte("It hits with a satisfying"),
    "rubber spider": []byte("You carefully hide the spider where"),
}

var kmailItemMatcher = regexp.MustCompile(`(?i)onClick=['"]descitem\((\d+)\)['"][^>]*>\s*</td>\s*<td[^>]*>You acquire (an item: <b>|<b>[0-9,.]+\s+)([^<]+)</b>`)
var holidayFunRe = regexp.MustCompile(`(?i)onclick=['"]descitem\(166504292\)["']>`)
func (bot *Chatbot)HandleKMail(playerID string, kmailID string) (string, error) {
    kol := bot.KoL
    b, err := kol.APIRequest("kmail", &map[string]string{
        "id": kmailID,
    })
    if err != nil {
        fmt.Println("kmail read error:", err)
        return "", err
    }

    kmail, err := kolgo.DecodeAPIKMail(b)
    if err != nil {
        fmt.Println("kmail decode error:", err)
        return "", err
    }

    msg   := kmail.Message
    items := msg.Items
    if msg.InnerMessage != nil {
        items = msg.InnerMessage.Items
    }

    itemsToCheck := []*kolgo.Item{}
    toDiscord    := ""
    for item, _ := range items {
        if _, ok := giftboxen[item.ID]; ok {
            body, err := kol.InvUse(item, 1)
            if err != nil {
                fmt.Println("error opening item: ", err)
                continue
            }

            if holidayFunRe.Match(body) {
                toDiscord = fmt.Sprintf("%s (#%s) sent the bot some holiday fun", kmail.From.Name, kmail.From.ID)
            }

            // Sigh... Either we parse html, or we need to re-request this kmail.  Let's try to be
            // nice and parse html.
            matches := kmailItemMatcher.FindAllStringSubmatch(string(body), -1)
            for _, m := range matches {
                descID   := m[1]
                itemName := m[3]
                item := kolgo.DescIDToItem(descID)
                if item == nil {
                    item, _ = kolgo.ToItem(itemName)
                }
                if item == nil {
                    // Too bad
                    fmt.Println("Could not figure out what item this is: ", m[0])
                    continue
                }
                itemsToCheck = append(itemsToCheck, item)
            }
        } else {
            itemsToCheck = append(itemsToCheck, item)
        }
    }

    id := kmail.From.ID
    for _, i := range itemsToCheck {
        fmt.Println("Was sent this item:", i.Name)
        successStr, ok := useOnSender[i.Name]
        if !ok {
            // Thanks for the donation, I guess?
            continue
        }

        body, err := kol.Curse(kmail.From.Name, i)
        if err != nil {
            fmt.Println("Error cursing player:", err)
            bot.KoL.SendMessage("/msg " + id, fmt.Sprintf("Could not use that %s on you because of an error", i.Name))
        }
        if !bytes.Contains(body, successStr) {
            fmt.Printf("Failed to use %s on %s: %s\n", i.Name, kmail.From.Name, string(body))
            bot.KoL.SendMessage("/msg " + id, fmt.Sprintf("Looks like you can't take another %s right now", i.Name))
        }
    }

    return toDiscord, nil
}

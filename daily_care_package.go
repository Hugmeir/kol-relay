package main

import (
    "fmt"
    "strings"
    "time"
    "bytes"
    "sync"
    "math/rand"
    "github.com/Hugmeir/kolgo"
)

func TodayDateString(who string) string {
    // No way I'm using the absolutely brain-dead .Format("2006") crap,
    // so we roll this ourselves
    y, m, d   := time.Now().Date()
    ident     := fmt.Sprintf("%d-%d-%d", y, int(m), d)
    return ident
}

func (bot *Chatbot) SeenTodayCount(today, who string) (int, error) {
    rows, err := bot.Db.Query("SELECT count FROM `daily_chat_seen` WHERE seen_date = ? and account_name = ?", today, who)
    if err != nil {
        fmt.Println("Failed to select the seen count:", err)
        return -1, err
    }
    defer rows.Close()

    for rows.Next() {
        var count int
        err := rows.Scan(&count)
        if err != nil {
            return -1, err
        }
        return count, nil
    }

    // We get here if we have yet to see them today
    return 0, nil
}

func (bot *Chatbot) IncreaseSeenTodayCount(today, who string) int {
    // Poor man's SELECT ... FOR UPDATE
    sqliteInsert.Lock()
    defer sqliteInsert.Unlock()

    seen, err := bot.SeenTodayCount(today, who)
    if err != nil {
        return -1
    }

    seen++

    stmt, err := bot.Db.Prepare("INSERT OR REPLACE INTO `daily_chat_seen` (`seen_date`, `account_name`, `seen_count`) VALUES (?, ?, ?)")
    if err != nil {
        fmt.Println("Failed to prepare insert for seen count:", err)
        return -1
    }
    defer stmt.Close()

    _, err = stmt.Exec(today, who, seen)
    if err != nil {
        fmt.Println("Failed to upsert the seen count:", err)
        return -1
    }

    return seen
}

// Global while we try out the care packages
var alreadySentToday sync.Map
const MINIMUM_CHATTERY_FOR_GIFTERY = 2
func (bot *Chatbot) MaybeSendCarePackage(who string) {
    if RunningInDevMode() {
        // Yeah... just don't do anything in dev mode
        return
    }

    // Just makes things simpler:
    who = strings.ToLower(who)

    if strings.EqualFold(who, `hugmeir`) {
        // Nope.  I'm opting myself out.  It smells of multi abuse to me.
        return
    }

    if strings.EqualFold(who, `hekiryuu`) {
        // Opting opt hekibot.  /baleet odebot goes deep.
        return
    }

    today := TodayDateString(who)
    if _, ok := alreadySentToday.Load(today + "|" + who); ok {
        // Already got to them, no need to lock the db
        return
    }

    seen := bot.IncreaseSeenTodayCount(today, who)
    if seen != MINIMUM_CHATTERY_FOR_GIFTERY {
        return
    }

    // Nice!  Let's send them a gift.
    alreadySentToday.Store(today + "|" + who, true)
    bot.SendCarePackage(who)
}

func CouldNotGetItem(b []byte) bool {
    if bytes.Contains(b, []byte(`You cannot take zero karma items from the stash`)) {
        return false
    }

    if bytes.Contains(b, []byte(`You don't have enough Clan Karma to take that item`)) {
        return false
    }

    if bytes.Contains(b, []byte(`There aren't that many of that item in the stash`)) {
        return false
    }

    return true
}

func (bot *Chatbot)TakeRandomItemFromList(items []*kolgo.Item) *kolgo.Item {
    item := items[rand.Intn(len(items))]
    // Take it out of the stash:
    body, err := bot.KoL.ClanTakeFromStash(item, 1)
    if err != nil {
        fmt.Println("Could not fetch item from stash due to an error:", err)
        return nil
    }

    if CouldNotGetItem(body) {
        return nil
    }

    return item
}

var crappyItems = map[string]bool{
    // Very crappy:
    "meat paste": true,
    "meat stack": true,
    "dense meat stack": true,

    // Fishing from the sewer:
    "seal-skull helmet": true,
    "seal-clubbing club": true,
    "helmet turtle": true,
    "turtle totem": true,
    "ravioli hat": true,
    "pasta spoon": true,
    "Hollandaise helmet": true,
    "saucepan": true,
    "disco mask": true,
    "disco ball": true,
    "mariachi hat": true,
    "stolen accordion": true,
    "old sweatpants": true,
    "worthless trinket": true,
    "worthless gewgaw": true,
    "worthless knick-knack": true,
}

func (bot *Chatbot) EligibleStashItems() []*kolgo.Item {
    bot.eligibleStashMutex.Lock()
    defer bot.eligibleStashMutex.Unlock()
    if len(bot.eligibleStashItems) != 0 {
        return bot.eligibleStashItems
    }

    body, err := bot.KoL.ClanStash()
    if err != nil {
        fmt.Println("Could not get stash list:", err)
        return nil
    }

    stash    := kolgo.DecodeClanStash(body)
    eligible := make([]*kolgo.Item, 0, len(stash))
    for _, i := range stash {
        if _, ok := crappyItems[i.Name]; ok {
            continue
        }

        eligible = append(eligible, i)
    }

    bot.eligibleStashItems = eligible
    return bot.eligibleStashItems
}

const FCA_CARE_PACKAGE = `That's the kind of attitude we like around here!

Or don't like.  This bot doesn't judge.

Either way, you were active on the FCA clan chat today, so you deserve a little reward:`
func (bot *Chatbot) SendCarePackage(who string) {
    eligibleItems := bot.EligibleStashItems()
    if len(eligibleItems) <= 0 {
        fmt.Println("Could not send a gift because the stash looks empty")
        return
    }

    item := bot.TakeRandomItemFromList(eligibleItems)
    if item == nil {
        // Do a second try
        item = bot.TakeRandomItemFromList(eligibleItems)
    }

    if item == nil {
        // Too bad, so sad
        fmt.Println("Could not take an item from the stash to gift to", who)
        return
    }

    items := &map[*kolgo.Item]int{
        item: 1,
    }
    body, err := bot.KoL.SendKMail(who, FCA_CARE_PACKAGE, 0, items)
    if err != nil {
        fmt.Println("Could not send package because of this error:", err)
        return
    }
    if bytes.Contains(body, []byte(`That player cannot receive`)) {
        // A package it is!
        bot.KoL.SendGift(who, FCA_CARE_PACKAGE, "Maybe it was a little punishment?", 0, items)
    }
}


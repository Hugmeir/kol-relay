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
    rows, err := bot.Db.Query("SELECT seen_count FROM `daily_chat_seen` WHERE seen_date = ? and account_name = ?", today, who)
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

// This is a blacklist of people that should never, ever get
// packages.  This is different from the opt-out feature -- there
// is no opting in for these.  They never get anything.
//
// This is basically reserved for people with admin access to
// the bot, to prevent even a whiff of multi abuse.
var carePackageBlacklist = map[string]string{
    // Admins:
    "hugmeir": "Wrote this sucker, getting packages smells strongly of multi abuse, so none for me, thanks.",
    "caducus": "Has the Relay's password in case I eat the bucket.  Yep, eat the bucket.  I don't intend to go any other way.",

    // Bots:
    "hekiryuu": "Is a bot, bots don't get presents. /baleet Odebot goes deep",
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

    if _, ok := carePackageBlacklist[who]; ok {
        // You don't get a package, sucker.
        return
    }

    today := TodayDateString(who)
    if _, ok := alreadySentToday.Load(today + "|" + who); ok {
        // Already got to them, no need to lock the db
        return
    }

    seen := bot.IncreaseSeenTodayCount(today, who)
    if seen == MINIMUM_CHATTERY_FOR_GIFTERY {
        // Nice!  Let's send them a gift.
        alreadySentToday.Store(today + "|" + who, true)
        bot.SendCarePackage(who)
    } else if seen > MINIMUM_CHATTERY_FOR_GIFTERY {
        // Another process got to it, so just mark it in-memory to
        // prevent db locks
        alreadySentToday.Store(today + "|" + who, true)
    }
}

func CouldGetItem(b []byte) bool {
    if bytes.Contains(b, []byte(`You acquire an item:`)) {
        return true
    }

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

    if !CouldGetItem(body) {
        if bytes.Contains(body, []byte(`You can't take that many more zero-karma items from the stash today.`)) {
            bot.ClearZeroKarmaItems()
        }
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

func (bot *Chatbot) ClearZeroKarmaItems() {
    bot.eligibleStashMutex.Lock()
    defer bot.eligibleStashMutex.Unlock()
    if len(bot.eligibleStashItems) == 0 {
        return
    }

    filtered := make([]*kolgo.Item, 0, len(bot.eligibleStashItems))
    for _, i := range bot.eligibleStashItems {
        if i.Autosell <= 0 {
            continue
        }
        filtered = append(filtered, i)
    }
    bot.eligibleStashItems = filtered
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

var carePackageNotes = [][2]string{
    [2]string{
        "That's the kind of attitude we like around here!\n\nOr don't like.  This bot doesn't judge.\n\nEither way, you were active on the FCA clan chat today, so you deserve a little reward:",
        "Maybe it was a little punishment?",
    },
    [2]string{
        "Since you shared in the FCA clan chat so candidly today (Probably.  This is a bot.  It doesn't know) we wanted to give you a little present.\n\nExperience the joy and wonder of this glorious mystery item:",
        "",
    },
    [2]string{
        "Congratulations! You interacted with -- allegedly -- human beings today, in the FCA clan chat.\n\nWe suspect it was a traumatic experience, so hopefully this item will make it better:",
        "",
    },
    [2]string{
        "Merely having a pulse is not enough to get a present, but talking in the FCA clan is!\n\nThis is a bot, it doesn't have standards.\n",
        "",
    },
    [2]string{
        // From Crui
        "Here is an item\nWith a star saying you tried\nNext time, do haiku.\n",
        "",
    },
    [2]string{
        "A little thank-you from us for talking in chat today:",
        "Genuinely hope it was worth the wait!",
    },
    [2]string{
        "You talking in clan chat today reminded me of this:",
        "",
    },
    [2]string{
        "This bot only exists because of chatterboxes like you.\n\nIndeed, you've aided in the continued existence of this terrible curse.\n\nPonder your actions with this:",
        "",
    },
    [2]string{
        "Hey now, you're an all-star.  Got your chat game on, get paid:",
        "Did this glitter? Was it gold?",
    },
    [2]string{
        "You talked in chat, and odds are you didn't kill Oscus today, so you are as close to a model clannie as it gets.",
        "",
    },
    [2]string{
        "Talking in chat gets you rewarded, but you know what's way more rewarding?  Sending packages to caducus while she's in-run.  Try it out!",
        "",
    },
    [2]string{
        "You've chatted hard enough to knock off a present off the clan ceiling.\n\nYou should chat harder, see if something else will get knocked off today.\n\n(Spoiler: it won't, but you never know)",
        "",
    },
    [2]string{
        "Meat that talks, just how unsettling is that?  Have a small reward for reducing the meat flapping and typing in clan chat today:",
        "http://www.mit.edu/people/dpolicar/writing/prose/text/thinkingMeat.html",
    },
    [2]string{
        "OYSTER FACT: Cultured pearls look just like natural ones but are considered less valuable.\nStop pearl discrimination!\n\nOh and you were active in chat today, so have a little pearl from the clan stash:",
        "",
    },
    [2]string{
        "Writing wholesome messages is hard, but you put the effort and sent messages in clan chat today, so... <3",
        "I-it's not like I like you or anything!",
    },
    [2]string{
        "Today, you succeeded in talking in /clan.  Huzzah!\n\nHere's your reward:",
        "",
    },
    [2]string{
        "As a branded talker, you must carry this item forever in penance.  Or until you get rid of it, whichever comes first:",
        "",
    },
}

// TODO: would be nice to do a dumb jumphash here so that
// a recipient won't get the same message twice.  But meh
func PickMessageText(who string) (string, string) {
    t := carePackageNotes[rand.Intn(len(carePackageNotes))]
    return t[0], t[1]
}

func (bot *Chatbot) SendCarePackage(who string) {
    eligibleItems := bot.EligibleStashItems()
    if len(eligibleItems) <= 0 {
        fmt.Println("Could not send a gift because the stash looks empty")
        return
    }

    item := bot.TakeRandomItemFromList(eligibleItems)
    if item == nil {
        // Do a second try; refresh the list:
        eligibleItems = bot.EligibleStashItems()
        if len(eligibleItems) > 0 {
            item = bot.TakeRandomItemFromList(eligibleItems)
        }
    }

    if item == nil {
        // Too bad, so sad
        fmt.Println("Could not take an item from the stash to gift to", who)
        return
    }

    fmt.Printf("Sending daily care package to %s: %s\n", who, item.Name)

    note, innernote := PickMessageText(who)

    items := &map[*kolgo.Item]int{
        item: 1,
    }
    body, err := bot.KoL.SendKMail(who, note, 0, items)
    if err != nil {
        fmt.Println("Could not send package because of this error:", err)
        return
    }
    if bytes.Contains(body, []byte(`That player cannot receive`)) {
        // A package it is!
        bot.KoL.SendGift(who, note, innernote, 0, items)
    }
}


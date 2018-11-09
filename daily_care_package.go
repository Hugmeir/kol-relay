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

// This is a blacklist of people that should never, ever get
// packages.  This is different from the opt-out feature -- there
// is no opting in for these.  They never get anything.
//
// This is basically reserved for people with admin access to
// the bot, to prevent even a whiff of multi abuse.  And whoever
// else I don't want to ever give packages to, I guess >.>
var hardBlacklist = map[string]bool{
    // Admins:
    `hugmeir`: true, // Wrote this sucker, getting packages smells strongly of multi abuse, so none for me, thanks.
    `caducus`: true, // Has the Relay's password in case I eat the bucket.  Yep, eat the bucket.  I don't intend to go any other way.

    // Bots:
    `hekiryuu`: true, // Is a bot, bots don't get presents. /baleet Odebot goes deep
}
func init() {
    for who, _ := range hardBlacklist {
        AddToPackageBlackList(who)
    }
}

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

func (bot *Chatbot) OptOutOfDailyPackages(who string) {
    who = strings.ToLower(who)
    if _, ok := carePackageBlacklist.Load(who); ok {
        // Someone wasting resources? No need to lock the db
        return
    }

    if _, ok := hardBlacklist[who]; ok {
        return
    }

    AddToPackageBlackList(who)

    sqliteInsert.Lock()
    defer sqliteInsert.Unlock()
    stmt, err := bot.Db.Prepare("INSERT OR REPLACE INTO `daily_package_opt_out` (`account_name`) VALUES (?)")
    if err != nil {
        fmt.Println("Failed to prepare insert for opt-out:", err)
        return
    }
    defer stmt.Close()
    _, _ = stmt.Exec(who)
    return
}

func (bot *Chatbot) OptOutOfOptingOutOfDailyPackages(who string) {
    who = strings.ToLower(who)
    if _, ok := carePackageBlacklist.Load(who); !ok {
        // Someone wasting resources? No need to lock the db
        return
    }

    if _, ok := hardBlacklist[who]; ok {
        // Nope.  You don't get out.
        return
    }

    carePackageBlacklist.Delete(who)

    sqliteInsert.Lock()
    defer sqliteInsert.Unlock()
    stmt, err := bot.Db.Prepare("DELETE FROM `daily_package_opt_out` WHERE `account_name`=?")
    if err != nil {
        fmt.Println("Failed to prepare delete for opt-out:", err)
        return
    }
    defer stmt.Close()
    _, _ = stmt.Exec(who)
    return
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

var carePackageBlacklist sync.Map
func AddToPackageBlackList(who string) {
    carePackageBlacklist.Store(who, true)
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

    if _, ok := carePackageBlacklist.Load(who); ok {
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
        "OYSTER FACT: The oyster you eat and the oyster that create pearls are actually different animals.  Assuming you eat oysters, at least.",
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
    [2]string{
        // Thanks Tim Minchin, you rock.
        "I worry that because a lot of the conversations in /clan are stupid, people will leave chat thinking we lack depth.  So to help with that a bit, ponder this:",
        "Wow, that was really deep, wasn't it?",
    },
    [2]string{
        "This chatroom might just be big enough for all of us -- but the clan stash is overflowing, so help the cause and take one of these:",
        "",
    },
    [2]string{
        "They say the secret to happiness starts with baleeting Odebot, but talking in chat is probably a close second!\n\nSo hopefully this will improve your day a bit:",
        "They also say that sending packages to people in-run brings joy.",
    },
    [2]string{
        "What the... How did you know? Talking in chat?\n\nI love that move!\n\nIf I could reward you for it, I would, so... Here you go!",
        "",
    },
    [2]string{
        "Look at this chat!\nEvery single time it makes me laugh\nHow did Odebot get so dunked\nAnd what the hell is that effect?\n\nOh and here's an item for talking today:",
        "(It appears that the chat being referenced was surreptitiously removed by bot sympathizers)",
    },
    [2]string{
        "This appears to be a placeholder message.  The developer will surely, one day, fill in this placeholder.  The real text will praise -- or berate! -- the message you just sent to clan chat.\n\nOoh, you would've been so elated and/or hurt.",
        "This is another placeholder message.  Looks like the developer spent a couple of minutes dissing Odebot here and then gave up.\n\nStay strong developer!",
    },
    [2]string{
        "9 out of 10 relays recommend talking in chat like you did today!",
        "We do not speak of the rogue relay.",
    },
    [2]string{
        "Talking in chat means that you've talked in chat.  And getting this item means you've gotten this item.",
        "Don't search for meaning in kmails -- We recommend fortune cookies instead.",
    },
    [2]string{
        "This bot rates your recent comments at around one of these:",
        "Maybe you should try harder in the future, and the future is now, since you had to hold on to that package.",
    },
    [2]string{
        "Wow, that was a close call, but you did it!\n\nSeriously, that was cutting it very close.  Was starting to think you didn't have it under control...\n\nAnyway, here's a little reward:",
        "Best if we don't tell anyone how close things got, they might freak out a bit.",
    },
    [2]string{
        "Did you know that Jenn donated the VIP Key that this bot uses?  That's why she rules, ok?\n\nWhat did you do today? Huh?\n\nOh, you talked in chat, fair enough.  Here's some goodies!",
        "",
    },
}

func PickMessageText(who string) (string, string) {
    // The player's name + current month is the RNG seed, so
    // every month they get a new set of greetings.  This
    // will be particularly useful once we have 60+ greetings
    _, month, today := time.Now().Date()
    var i int64 = int64(month)
    for _, r := range who {
        i += int64(r)
    }

    seed := rand.NewSource(i)
    r    := rand.New(seed)

    // Shuffle the greetings using our shiny new RNG
    shuffled := make([][2]string, len(carePackageNotes))
    copy(shuffled, carePackageNotes)
    r.Shuffle(len(carePackageNotes), func (i, j int) {
        shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
    })

    // Pick our winner:
    t := shuffled[ today % len(shuffled) ]
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


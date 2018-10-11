package main

import (
    "fmt"
    "strconv"
    "encoding/json"
    "github.com/Hugmeir/kolgo"
)

type KoLInventory map[*kolgo.Item]int

func MakeKoLInventory() KoLInventory {
    return KoLInventory{}
}

func (bot *Chatbot) IncItem(i *kolgo.Item, c int) {
    bot.InventoryMutex.Lock()
    defer bot.InventoryMutex.Unlock()
    bot.Inventory[i] += c
}

func (bot *Chatbot) DecItem(i *kolgo.Item, c int) {
    bot.InventoryMutex.Lock()
    defer bot.InventoryMutex.Unlock()
    bot.Inventory[i] -= c
    if bot.Inventory[i] <= 0 {
        delete(bot.Inventory, i)
    }
}

func (bot *Chatbot) RefreshInventory() KoLInventory {
    kol := bot.KoL
    body, err := kol.APIRequest("inventory", nil)
    if err != nil {
        fmt.Println("Could not refresh inventory!", err)
        return bot.Inventory
    }

    var rawInv map[string]interface{}
    err = json.Unmarshal(body, &rawInv)
    if err != nil {
        fmt.Println("Error decoding the inventory: ", err)
        return bot.Inventory
    }

    bot.InventoryMutex.Lock()
    defer bot.InventoryMutex.Unlock()
    newInv := MakeKoLInventory()
    for k, v := range rawInv {
        i, err := strconv.Atoi(k)
        if err != nil {
            continue
        }
        item, _ := kolgo.ToItem(i)
        if item == nil {
            fmt.Printf("Unknown item '%s' will be ignored\n", k)
            continue
        }

        var amount int
        switch v.(type) {
            case string:
                amount, _ = strconv.Atoi(v.(string))
            default:
                amount = v.(int)
        }
        newInv[item] = amount
    }
    bot.Inventory = newInv
    return newInv
}


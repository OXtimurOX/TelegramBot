package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

type Account struct {
	Email       string
	Password    string
	Name        string
	HomeworkURL string
}

type Homework struct {
	Link string `json:"link"`
	Type string `json:"type"`
}

type Storage map[string][]string

const (
	botToken = "8396719135:AAG3k-GI3jU0RnyHrRUMzQ-YsSDHILOdKUw"
	chatID   = "1470084510"
)

func main() {
	accounts := []Account{
		{"matmasha.VESNA11@mail.ru", "goel2026", "Account1", "https://pl.el-ed.ru/clan/5161/homeworks"},
		{"matmasha.VESNA10@mail.ru", "goel2026", "Account2", "https://pl.el-ed.ru/clan/5164/homeworks"},
		{"matsashaVESNA11@mail.ru", "goel2026", "Account3", "https://pl.el-ed.ru/clan/5165/homeworks"},
		{"matsashaVESNA10@mail.ru", "goel2026", "Account4", "https://pl.el-ed.ru/clan/5167/homeworks"},
	}

	for {
		fmt.Println("Проверка:", time.Now().Format("15:04:05"))

		storage := loadStorage()

		for _, acc := range accounts {
			checkAccount(acc, storage)
		}

		saveStorage(storage)

		fmt.Println("Ждём 10 минут...")
		time.Sleep(10 * time.Minute)
	}
}

func checkAccount(acc Account, storage Storage) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.WindowSize(1920, 1080),
		// chromedp.Flag("disable-gpu", true),
		// chromedp.Flag("enable-automation", false),
		// chromedp.Flag("disable-blink-features", "AutomationControlled"),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	var homeworks []Homework

	err := chromedp.Run(ctx,
		chromedp.Evaluate(`Object.defineProperty(navigator, 'webdriver', {get: () => undefined})`, nil),
		chromedp.Navigate("https://pl.el-ed.ru/auth"),
		chromedp.Sleep(8*time.Second),

		chromedp.Click(`//button[contains(text(),"Понятно, согласен")]`, chromedp.BySearch),
		chromedp.Sleep(2*time.Second),

		chromedp.Click(`//button[contains(text(),"Войти по почте")]`, chromedp.BySearch),
		chromedp.SendKeys(`input[type="email"]`, acc.Email),
		chromedp.SendKeys(`input[type="password"]`, acc.Password),
		chromedp.Click(`button[type="submit"]`),

		chromedp.Sleep(5*time.Second),

		chromedp.Navigate(acc.HomeworkURL),
		chromedp.Sleep(7*time.Second),
		chromedp.Reload(),
		chromedp.Sleep(10*time.Second),
		chromedp.Evaluate(`
Array.from(document.querySelectorAll('a[href^="/homework-done/"]')).map(a => {
 const blocks = Array.from(a.querySelectorAll('div'));
 return {
  link: a.getAttribute("href"),
  type: blocks.map(b => b.innerText).join(" ")
 }
})
  `, &homeworks),
	)
	if err != nil {
		log.Println("Ошибка:", err)
		sendTelegram("❌ " + acc.Name + " — ошибка входа")
		return
	}

	fmt.Println("Найдено карточек:", len(homeworks))

	newFound := false
	var messageLines []string

	for _, hw := range homeworks {

		fmt.Println("Проверяем:", hw.Link)
		fmt.Println("Текст:", hw.Type)

		if strings.Contains(hw.Type, "Пробник, математика") ||
			strings.Contains(hw.Type, "Первая часть, математика") ||
			strings.Contains(hw.Type, "Первая и вторая части, математика") {

			if !contains(storage[acc.Name], hw.Link) {

				newFound = true
				storage[acc.Name] = append(storage[acc.Name], hw.Link)

				messageLines = append(messageLines,
					"🔹 "+hw.Type+"\nhttps://pl.el-ed.ru"+hw.Link)
			}
		}
	}

	if newFound {
		sendTelegram("🔥 " + acc.Name + "\nНовые ДЗ:\n\n" +
			strings.Join(messageLines, "\n\n"))
	} //else {
		//sendTelegram("✅ " + acc.Name + " — новых домашних заданий нет")
	//}
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func sendTelegram(message string) {
	escaped := url.QueryEscape(message)
	apiURL := fmt.Sprintf(
		"https://api.telegram.org/bot%s/sendMessage?chat_id=%s&text=%s",
		botToken,
		chatID,
		escaped,
	)

	resp, err := http.Get(apiURL)
	if err != nil {
		log.Println("Ошибка Telegram:", err)
		return
	}
	defer resp.Body.Close()
}

func loadStorage() Storage {
	file, err := os.ReadFile("storage.json")
	if err != nil {
		return make(Storage)
	}
	var storage Storage
	json.Unmarshal(file, &storage)
	return storage
}

func saveStorage(storage Storage) {
	data, _ := json.MarshalIndent(storage, "", "  ")
	os.WriteFile("storage.json", data, 0644)
}

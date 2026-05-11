package main

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	_ "github.com/lib/pq"
)

type Account struct {
	Email       string
	Password    string
	Name        string
	HomeworkURL string
	ChatID      string
}

type Homework struct {
	Link string `json:"link"`
	Text string `json:"text"`
}

func main() {
	// чтобы Railway не убивал сервис
	go func() {
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}
		http.ListenAndServe(":"+port, nil)
	}()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is missing")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`
 CREATE TABLE IF NOT EXISTS saved_homeworks (
  account VARCHAR(100),
  link TEXT,
  UNIQUE(account, link)
 );`)
	if err != nil {
		log.Fatal(err)
	}

	accounts := []Account{
		{
			"6probnikm@mail.ru",
			"goelprobe",
			"МАША",
			"https://pl.el-ed.ru/clan/5294/homeworks",
			os.Getenv("TELEGRAM_CHAT_ID_1"), // 👈 первый человек
		},
		{
			"7probnikm@mail.ru",
			"goelprobe",
			"САША",
			"https://pl.el-ed.ru/clan/5293/homeworks",
			os.Getenv("TELEGRAM_CHAT_ID_1"), // 👈 второй человек
		},
		{
			"2probnikm@mail.ru",
			"goelprobe",
			"AСЯ",
			"https://pl.el-ed.ru/clan/5298/homeworks",
			os.Getenv("TELEGRAM_CHAT_ID_2"), // 👈 второй человек
		},
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.NoSandbox,
		chromedp.DisableGPU,
		chromedp.Headless,
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("no-zygote", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.WindowSize(1920, 1080),
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/124.0.0.0 Safari/537.36"),
	)

	for {
		fmt.Println("=== Старт проверки:", time.Now().Format("15:04:05"), "===")

		allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
		defer cancelAlloc()

		browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
		defer cancelBrowser()

		for {
			fmt.Println("=== Старт проверки:", time.Now().Format("15:04:05"), "===")

			for _, acc := range accounts {
				ctx, cancel := chromedp.NewContext(browserCtx)

				checkAccount(ctx, acc, db)

				cancel()

				time.Sleep(10 * time.Second)
			}

			fmt.Println("⏸️ Ждем 30 секунд...")
			time.Sleep(15 * time.Second)
		}
	}
}

func extractID(link string) string {
	if link == "" {
		return ""
	}

	parts := strings.Split(link, "/")
	return parts[len(parts)-1]
}

func makeHash(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

func normalizeText(s string) string {
	s = strings.ToLower(s)

	// ❌ убираем "63", "64", "65" (часы)
	reHours := regexp.MustCompile(`\b\d{1,3}\b`)
	s = reHours.ReplaceAllString(s, "")

	// ❌ убираем дату типа "04 мая 15:21"
	reDate := regexp.MustCompile(`\d{2}\s+\p{L}+\s+\d{2}:\d{2}`)
	s = reDate.ReplaceAllString(s, "")

	// ❌ убираем статус
	s = strings.ReplaceAll(s, "ожидает проверки", "")
	s = strings.ReplaceAll(s, "проверено", "")

	// ❌ убираем лишние пробелы
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")

	return strings.TrimSpace(s)
}

func checkAccount(ctx context.Context, acc Account, db *sql.DB) {
	timeCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	var homeworks []Homework

	log.Printf("[%s] Вхожу...", acc.Name)

	err := chromedp.Run(timeCtx,
		chromedp.Navigate("https://pl.el-ed.ru/auth"),
		chromedp.Sleep(10*time.Second),

		chromedp.ActionFunc(func(ctx context.Context) error {
			ctx2, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()

			err := chromedp.Click(
				`//button[contains(text(),"Понятно, согласен")]`,
				chromedp.BySearch,
			).Do(ctx2)
			if err != nil {
				log.Println("Кнопка согласия не найдена, пропускаем...")
			}

			return nil
		}),

		chromedp.Click(`//button[contains(., "Войти по почте")]`, chromedp.BySearch),
		chromedp.WaitVisible(`input[type="email"]`),

		chromedp.SendKeys(`input[type="email"]`, acc.Email),
		chromedp.SendKeys(`input[type="password"]`, acc.Password),
		chromedp.Click(`button[type="submit"]`),

		chromedp.Sleep(10*time.Second),

		chromedp.Navigate(acc.HomeworkURL),

		// ждём загрузку
		chromedp.Sleep(15*time.Second),

		// скролл
		chromedp.ActionFunc(func(ctx context.Context) error {
			for i := 0; i < 5; i++ {
				chromedp.Evaluate(`
     window.scrollBy(0, 1000);
     document.querySelectorAll('*').forEach(el => {
      if (el.scrollHeight > el.clientHeight) el.scrollTop += 1000;
     });
    `, nil).Do(ctx)
				time.Sleep(2 * time.Second)
			}
			return nil
		}),

		// парсинг
		chromedp.Evaluate(`
Array.from(document.querySelectorAll('tr')).map(tr => {
 let a = tr.querySelector('a');
 return {
  link: a ? a.getAttribute("href") : "",
  text: tr.innerText.replace(/\s+/g, ' ').trim()
 }
}).filter(h => h.text.length > 10)
  `, &homeworks),
	)
	if err != nil {
		log.Printf("[%s] Ошибка: %v", acc.Name, err)
		return
	}

	newFound := false
	var msg []string

	for _, hw := range homeworks {
		txt := strings.ToLower(hw.Text)

		isMath := strings.Contains(txt, "математика") || strings.Contains(txt, "мат") || strings.Contains(txt, "русский ЕГЭ") || strings.Contains(txt, "рус") || strings.Contains(txt, "ЕГЭ")
		isProbnik := strings.Contains(txt, "пробник") || strings.Contains(txt, "проб")
		isPart := strings.Contains(txt, "часть") || strings.Contains(txt, "части")

		if isMath && (isProbnik || isPart) {

			finalLink := hw.Link
			if finalLink == "" || finalLink == "#" || strings.Contains(finalLink, "javascript") {
				finalLink = acc.HomeworkURL
			} else if !strings.HasPrefix(finalLink, "http") {
				finalLink = "https://pl.el-ed.ru" + finalLink
			}

			// 🔥 ВАЖНО: уникальный ключ
			cleanText := normalizeText(hw.Text)
			base := cleanText
			dbKey := makeHash(base)
			res, err := db.Exec(`
INSERT INTO saved_homeworks (account, link)
VALUES ($1, $2)
ON CONFLICT DO NOTHING
   `, acc.Name, dbKey)
			if err != nil {
				log.Printf("[%s] DB ошибка: %v", acc.Name, err)
				continue
			}

			aff, _ := res.RowsAffected()

			if aff > 0 {
				newFound = true

				msg = append(msg,
					"🔹 Найдена работа:\n"+hw.Text+"\n\nСсылка: "+finalLink)

				log.Printf("[%s] ✅ Новая работа добавлена", acc.Name)
			}
		}
	}

	if newFound {
		sendTelegram("🔥 "+acc.Name+"\nНовые работы:\n\n"+strings.Join(msg, "\n\n---\n"), acc.ChatID)
	} else {
		log.Printf("[%s] Новых работ нет", acc.Name)
	}
}

func sendTelegram(message string, chatID string) {
	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")

	if botToken == "" || chatID == "" {
		log.Println("Telegram env пустые")
		return
	}

	apiURL := fmt.Sprintf(
		"https://api.telegram.org/bot%s/sendMessage?chat_id=%s&text=%s",
		botToken,
		chatID,
		url.QueryEscape(message),
	)

	resp, err := http.Get(apiURL)
	if err != nil {
		log.Println("Telegram error:", err)
		return
	}
	defer resp.Body.Close()
}

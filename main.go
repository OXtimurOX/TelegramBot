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
		hash TEXT,
		UNIQUE(account, hash)
	);
	`)
	if err != nil {
		log.Fatal(err)
	}

	accounts := []Account{
		// 👉 эти два одному человеку
		{"6probnikm@mail.ru", "goelprobe", "МАША", "https://pl.el-ed.ru/clan/5294/homeworks", "CHAT_ID_1"},
		{"7probnikm@mail.ru", "goelprobe", "САША", "https://pl.el-ed.ru/clan/5293/homeworks", "CHAT_ID_1"},

		// 👉 этот другому
		{"2probnikm@mail.ru", "goelprobe", "АСЯ", "https://pl.el-ed.ru/clan/5298/homeworks", "CHAT_ID_2"},
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
	)

	// ✅ один браузер
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

		fmt.Println("⏸️ Ждем 5 минут...")
		time.Sleep(5 * time.Minute)
	}
}

// 🔥 УБИРАЕМ ЧАСЫ / ДАТЫ → чтобы не было дублей
func normalizeText(s string) string {
	s = strings.ToLower(s)

	// удалить часы (63, 64 и т.д.)
	reHours := regexp.MustCompile(`\b\d{1,3}\b`)
	s = reHours.ReplaceAllString(s, "")

	// удалить дату
	reDate := regexp.MustCompile(`\d{2}\s+\p{L}+\s+\d{2}:\d{2}`)
	s = reDate.ReplaceAllString(s, "")

	// удалить статус
	s = strings.ReplaceAll(s, "ожидает проверки", "")
	s = strings.ReplaceAll(s, "просмотрено", "")
	s = strings.ReplaceAll(s, "проверено", "")

	// убрать лишние пробелы
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")

	return strings.TrimSpace(s)
}

func makeHash(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

func checkAccount(ctx context.Context, acc Account, db *sql.DB) {
	timeCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	var homeworks []Homework

	log.Printf("[%s] Вхожу...", acc.Name)

	err := chromedp.Run(timeCtx,
		chromedp.Navigate("https://pl.el-ed.ru/auth"),
		chromedp.Sleep(5*time.Second),

		chromedp.Click(`//button[contains(text(),"Понятно, согласен")]`, chromedp.BySearch, chromedp.AtLeast(0)),
		chromedp.Sleep(2*time.Second),

		chromedp.Click(`//button[contains(., "Войти по почте")]`, chromedp.BySearch),
		chromedp.WaitVisible(`input[type="email"]`),

		chromedp.SendKeys(`input[type="email"]`, acc.Email),
		chromedp.SendKeys(`input[type="password"]`, acc.Password),
		chromedp.Click(`button[type="submit"]`),

		chromedp.Sleep(10*time.Second),

		chromedp.Navigate(acc.HomeworkURL),
		chromedp.Sleep(15*time.Second),

		// скролл
		chromedp.ActionFunc(func(ctx context.Context) error {
			for i := 0; i < 5; i++ {
				chromedp.Evaluate(`window.scrollBy(0, 1000);`, nil).Do(ctx)
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
}).filter(h => h.text.length > 20)
`, &homeworks),
	)

	if err != nil {
		log.Printf("[%s] Ошибка: %v", acc.Name, err)
		return
	}

	for _, hw := range homeworks {
		txt := strings.ToLower(hw.Text)

		isMath := strings.Contains(txt, "мат")
		isProbnik := strings.Contains(txt, "пробник")
		isPart := strings.Contains(txt, "часть")

		if isMath && (isProbnik || isPart) {

			finalLink := hw.Link
			if finalLink == "" || strings.Contains(finalLink, "javascript") {
				finalLink = acc.HomeworkURL
			} else if !strings.HasPrefix(finalLink, "http") {
				finalLink = "https://pl.el-ed.ru" + finalLink
			}

			// 🔥 УНИКАЛЬНОСТЬ
			clean := normalizeText(hw.Text)
			hash := makeHash(clean)

			res, err := db.Exec(`
INSERT INTO saved_homeworks (account, hash)
VALUES ($1, $2)
ON CONFLICT DO NOTHING
`, acc.Name, hash)

			if err != nil {
				continue
			}

			aff, _ := res.RowsAffected()

			if aff > 0 {
				// ✅ КАЖДАЯ РАБОТА ОТДЕЛЬНО
				message := fmt.Sprintf(
					"%s\n\n%s\n\n%s",
					acc.Name,
					hw.Text,
					finalLink,
				)

				sendTelegram(message, acc.ChatID)

				log.Printf("[%s] ✅ Отправлено", acc.Name)
			}
		}
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
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
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
}

type Homework struct {
	Link string `json:"link"`
	Text string `json:"text"`
}

func main() {
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
		{"matmasha.VESNA11@mail.ru", "goel2026", "Account1", "https://pl.el-ed.ru/clan/5161/homeworks"},
		{"matmasha.VESNA10@mail.ru", "goel2026", "Account2", "https://pl.el-ed.ru/clan/5164/homeworks"},
		{"matsashaVESNA11@mail.ru", "goel2026", "Account3", "https://pl.el-ed.ru/clan/5165/homeworks"},
		{"matsashaVESNA10@mail.ru", "goel2026", "Account4", "https://pl.el-ed.ru/clan/5167/homeworks"},
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.NoSandbox,
		chromedp.DisableGPU,
		chromedp.Headless,
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.WindowSize(1920, 1080),
	)

	for {
		fmt.Println("=== Старт проверки:", time.Now().Format("15:04:05"), "===")
		for _, acc := range accounts {
			allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
			ctx, cancelCtx := chromedp.NewContext(allocCtx)

			checkAccount(ctx, acc, db)

			cancelCtx()
			cancelAlloc()

			time.Sleep(10 * time.Second)
		}
		fmt.Println("⏸️ Пауза 10 минут...")
		time.Sleep(10 * time.Minute)
	}
}

func checkAccount(ctx context.Context, acc Account, db *sql.DB) {
	timeCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	var homeworks []Homework
	log.Printf("[%s] Вхожу в аккаунт...", acc.Name)

	err := chromedp.Run(timeCtx,
		chromedp.Navigate("https://pl.el-ed.ru/auth"),
		chromedp.Sleep(5*time.Second),
		chromedp.Click(`//button[contains(text(),"Понятно, согласен")]`, chromedp.BySearch, chromedp.AtLeast(0)),
		chromedp.Click(`//button[contains(., "Войти по почте")]`, chromedp.BySearch),
		chromedp.WaitVisible(`input[type ="email"]`),
		chromedp.SendKeys(`input[type="email"]`, acc.Email),
		chromedp.SendKeys(`input[type="password"]`, acc.Password),
		chromedp.Click(`button[type="submit"]`),
		chromedp.Sleep(10*time.Second),

		chromedp.Navigate(acc.HomeworkURL),
		chromedp.Sleep(10*time.Second), // Ждем загрузки каркаса

		// БЕШЕНЫЙ СКРОЛЛИНГ: Скроллим ВСЁ, что может скроллиться на странице (включая саму таблицу)
		chromedp.ActionFunc(func(ctx context.Context) error {
			for i := 0; i < 6; i++ {
				chromedp.Evaluate(`
     // Крутим основное окно
     window.scrollBy(0, 1000);
     // Ищем любые внутренние блоки с прокруткой (таблицы) и крутим их
     document.querySelectorAll('*').forEach(el => {
      if (el.scrollHeight > el.clientHeight) {
       el.scrollTop += 1000;
      }
     });
    `, nil).Do(ctx)
				time.Sleep(2 * time.Second) // Даем время подгрузить данные
			}
			return nil
		}),

		// УМНЫЙ ПОИСК: Ищем ссылку, но текст берем из всей строки таблицы (<tr>)
		chromedp.Evaluate(`
   Array.from(document.querySelectorAll('a')).map(a => {
    // Если ссылка внутри таблицы, берем текст всей строки. Иначе текст самой ссылки.
    let container = a.closest('tr') || a.parentElement.parentElement;
    let rowText = container ? container.innerText : a.innerText;
    
    return {
     link: a.getAttribute("href") || "",
     text: rowText.toLowerCase().replace(/\s+/g, ' ')
    }
   }).filter(h => h.link !== "" && !h.link.includes("javascript"))
  `, &homeworks),
	)
	if err != nil {
		log.Printf("[%s] Ошибка: %v", acc.Name, err)
		return

	}

	newFound := false
	var msg []string

	for _, hw := range homeworks {
		// Отсекаем ссылки, которые просто открывают меню/курсы слева
		if !strings.Contains(hw.Link, "homework") && !strings.Contains(hw.Link, "task") {
			continue
		}

		// ЖЕСТКИЙ ПОИСК ПО СЛОВАМ В СТРОКЕ
		txt := hw.Text
		isMath := strings.Contains(txt, "Пробник, математика")
		isProbnik := strings.Contains(txt, "Первая и вторая части, математика")
		isPart := strings.Contains(txt, "часть") || strings.Contains(txt, "части")

		// Ищем совпадение: должна быть Математика + (Пробник ИЛИ Часть)
		if isMath && (isProbnik || isPart) {

			res, err := db.Exec(`INSERT INTO saved_homeworks (account, link) VALUES ($1, $2) ON CONFLICT DO NOTHING`, acc.Name, hw.Link)
			if err != nil {
				log.Printf("[%s] Ошибка БД: %v", acc.Name, err)
				continue
			}

			aff, _ := res.RowsAffected()
			if aff > 0 {
				newFound = true
				fullLink := hw.Link
				if !strings.HasPrefix(fullLink, "http") {
					fullLink = "https://pl.el-ed.ru" + fullLink
				}

				// Выводим в лог, что конкретно нашли, чтобы было понятно
				log.Printf("[%s] НАШЁЛ РАБОТУ! Ссылка: %s", acc.Name, fullLink)

				// Сокращаем текст для сообщения в Telegram
				displayTitle := "Математика: Работа на проверку"
				if isProbnik {
					displayTitle = "Математика: Пробник"
				} else if isPart {
					displayTitle = "Математика: Часть 1/2"
				}

				msg = append(msg, "🔹 "+displayTitle+"\n"+fullLink)
			}
		}
	}

	if newFound {
		sendTelegram("🔥 " + acc.Name + "\nНашёл новые ДЗ в таблице:\n\n" + strings.Join(msg, "\n\n"))
	} else {
		log.Printf("[%s] В таблице ничего нового по фильтру не найдено", acc.Name)
	}
}

func sendTelegram(message string) {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage?chat_id=%s&text=%s", os.Getenv("TELEGRAM_BOT_TOKEN"), os.Getenv("TELEGRAM_CHAT_ID"), url.QueryEscape(message))
	http.Get(apiURL)
}

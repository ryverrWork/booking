package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"

	"golang.org/x/net/html"
)

func main() {
	client, err := createHTTPClient()
	if err != nil {
		fmt.Println("Error creating HTTP client:", err)
		return
	}

	params := url.Values{
		"origination": {"PSE"},
		"destination": {"LPN"},
		"tanggal":     {"05-Maret-2025"},
		"adult":       {"1"},
		"infant":      {"0"},
	}

	requiredInputs := map[string]string{
		"kereta":        "BENGAWAN",
		"kelas":         "C",
		"kelas_gerbong": "EKO",
	}

	baseURL := "https://booking.kai.id/"
	reqURL := baseURL + "?" + params.Encode()

	// Step 1: Ambil halaman pencarian tiket
	resp, err := sendRequest(client, reqURL)
	if err != nil {
		fmt.Println("Request error:", err)
		return
	}
	defer resp.Body.Close()

	printCookies(client, baseURL)

	// Step 2: Cari form yang sesuai dan submit ke halaman passengerdata
	passengerDataURL, err := findMatchingForm(client, resp.Body, requiredInputs)
	if err != nil {
		fmt.Println("Error processing form:", err)
		return
	}

	printCookies(client, passengerDataURL)

	// Step 3: Ambil `_token` dan captcha dari halaman `/passengerdata`
	_token, captchaURL, err := extractTokenAndCaptcha(client, passengerDataURL)
	if err != nil {
		fmt.Println("Error fetching token and captcha:", err)
		return
	}

	// Step 4: Simpan captcha untuk dilihat pengguna
	err = saveCaptcha(client, captchaURL)
	if err != nil {
		fmt.Println("Error saving captcha:", err)
		return
	}

	// Step 5: Debugging - Tampilkan semua cookie untuk memastikan sesi tetap ada
	printCookies(client, captchaURL)

	// Step 6: Minta input captcha dari terminal
	captchaInput := promptForCaptcha()

	// Step 7: Submit form `/passengercontrol`
	formInputs := url.Values{
		"_token":                      {_token},
		"pemesan_email":               {"test@mail.com"},
		"pemesan_nohp":                {"12345678"},
		"pemesan_alamat":              {"asal"},
		"penumpang_nohp[]":            {"12345678"},
		"penumpang_nama[]":            {"ini budi"},
		"penumpang_type[]":            {"A"},
		"penumpang_notandapengenal[]": {"3602041211870001"},
		"captcha":                     {captchaInput},
	}

	passengerControlURL := "https://booking.kai.id/passengercontrol"

	// Mencetak input yang dikirim
	fmt.Println("📩 Input yang dikirim:")
	for key, value := range formInputs {
		fmt.Printf("%s: %s\n", key, value)
	}

	fmt.Printf("Passenger Control URL: %s", passengerControlURL)

	resp, err = submitForm(client, passengerControlURL, "POST", formInputs)
	if err != nil {
		fmt.Println("Error submitting form:", err)
		return
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	fmt.Println("📩 Response dari Passenger Control:\n", string(bodyBytes))
}

// Membuat HTTP Client dengan Cookie Jar untuk sesi
func createHTTPClient() (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &http.Client{Jar: jar}, nil
}

// Mengirim request HTTP GET dengan referer
func sendRequest(client *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Referer", "https://booking.kai.id/passengerdata")

	return client.Do(req)
}

// Menemukan form yang cocok dan submit ke halaman passengerdata
func findMatchingForm(client *http.Client, body io.Reader, requiredInputs map[string]string) (string, error) {
	doc := html.NewTokenizer(body)
	var formAction string
	formInputs := make(map[string]string)
	insideForm := false

	for {
		switch doc.Next() {
		case html.ErrorToken:
			return "", fmt.Errorf("form tidak ditemukan")
		case html.StartTagToken:
			token := doc.Token()
			switch token.Data {
			case "form":
				insideForm, formInputs, formAction = true, make(map[string]string), ""
				for _, attr := range token.Attr {
					if attr.Key == "action" {
						formAction = attr.Val
					}
				}
			case "input":
				if insideForm {
					name, value := extractInputAttributes(token.Attr)
					if name != "" {
						formInputs[name] = value
					}
				}
			}
		case html.EndTagToken:
			token := doc.Token()
			if insideForm && token.Data == "form" {
				insideForm = false
				if formMatches(formInputs, requiredInputs) {
					fmt.Println("✅ Form pertama cocok! Mengarahkan ke:", formAction)
					return formAction, nil
				}
			}
		}
	}
}

// Mengambil `_token` dan URL captcha dari halaman `/passengerdata`
func extractTokenAndCaptcha(client *http.Client, url string) (string, string, error) {
	resp, err := sendRequest(client, url)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	doc := html.NewTokenizer(resp.Body)
	var _token, captchaURL string

	for {
		switch doc.Next() {
		case html.ErrorToken:
			return "", "", fmt.Errorf("token atau captcha tidak ditemukan")
		case html.StartTagToken:
			token := doc.Token()
			if token.Data == "input" {
				name, value := extractInputAttributes(token.Attr)
				if name == "_token" {
					_token = value
				}
			}
			if token.Data == "img" {
				for _, attr := range token.Attr {
					if attr.Key == "src" && strings.Contains(attr.Val, "captcha") {
						if strings.HasPrefix(attr.Val, "http") {
							captchaURL = attr.Val
						} else {
							captchaURL = "https://booking.kai.id" + attr.Val
						}

					}
				}
			}
		}
		if _token != "" && captchaURL != "" {
			break
		}
	}

	if _token == "" || captchaURL == "" {
		return "", "", fmt.Errorf("gagal menemukan _token atau captcha")
	}

	return _token, captchaURL, nil
}

// Menampilkan semua cookie yang tersimpan (Debugging)
func printCookies(client *http.Client, urlStr string) {
	parsedURL, _ := url.Parse(urlStr)
	fmt.Println("🍪 Cookies yang tersimpan:")
	for _, cookie := range client.Jar.Cookies(parsedURL) {
		fmt.Println(cookie.Name, "=", cookie.Value)
	}
}

// Menyimpan gambar captcha ke file dan memperbarui cookies
func saveCaptcha(client *http.Client, captchaURL string) error {
	fmt.Println("🖼️ Downloading captcha...")

	// Ambil cookies sebelum request captcha
	parsedURL, _ := url.Parse(captchaURL)
	cookiesBefore := client.Jar.Cookies(parsedURL) // Preserve current cookies

	// Lakukan request ke captcha
	resp, err := sendRequest(client, captchaURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Simpan gambar captcha
	file, err := os.Create("captcha.png")
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return err
	}

	fmt.Println("✅ Captcha saved as captcha.png")

	// Restore the cookies to the state they were in before the captcha request
	client.Jar.SetCookies(parsedURL, cookiesBefore)

	// Periksa cookies setelah request captcha (seharusnya tidak berubah)
	printCookies(client, captchaURL)

	return nil
}

// Meminta input captcha dari terminal
func promptForCaptcha() string {
	fmt.Print("📝 Masukkan teks captcha: ")
	var captcha string
	fmt.Scanln(&captcha)
	return captcha
}

// Submit form ke server
func submitForm(client *http.Client, action, method string, formInputs url.Values) (*http.Response, error) {
	req, err := http.NewRequest(method, action, strings.NewReader(formInputs.Encode()))
	if err != nil {
		return nil, fmt.Errorf("❌ Gagal membuat request: %v", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "*/*")

	return client.Do(req)
}

// Mengekstrak atribut `name` dan `value` dari input form
func extractInputAttributes(attrs []html.Attribute) (string, string) {
	var name, value string
	for _, attr := range attrs {
		if attr.Key == "name" {
			name = attr.Val
		} else if attr.Key == "value" {
			value = attr.Val
		}
	}
	return name, value
}

// Mengecek apakah form cocok dengan kriteria yang diberikan
func formMatches(formInputs, requiredInputs map[string]string) bool {
	for key, val := range requiredInputs {
		if formInputs[key] != val {
			return false
		}
	}
	return true
}

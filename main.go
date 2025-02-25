package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
	"sync"
	"errors"
)

// SectionWriter реализует запись в определенную секцию Writer'а
type SectionWriter struct {
	W    io.Writer // основной writer
	Base int64     // начальная позиция
	Size int64     // максимальный размер секции
	Off  int64     // текущее смещение
}

// Write реализует интерфейс io.Writer для SectionWriter
func (s *SectionWriter) Write(p []byte) (n int, err error) {
	if s.Off >= s.Size {
		return 0, errors.New("превышен размер секции")
	}
	if max := s.Size - s.Off; int64(len(p)) > max {
		p = p[:max]
	}
	
	// Если основной writer поддерживает WriteAt, используем его
	if w, ok := s.W.(io.WriterAt); ok {
		n, err = w.WriteAt(p, s.Base+s.Off)
	} else {
		// Иначе используем обычный Write (подходит только для последовательной записи)
		n, err = s.W.Write(p)
	}
	
	s.Off += int64(n)
	return
}

// Функция для скачивания части файла
func downloadChunk(url string, start, end int64, output *os.File, wg *sync.WaitGroup) error {
	defer wg.Done()

	client := &http.Client{
		Timeout: 10 * time.Minute,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  true, // Отключаем сжатие для ускорения
		},
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("ошибка при создании запроса: %v", err)
	}

	// Добавляем заголовки для имитации браузера
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	req.Header.Set("Accept", "video/mp4,video/*;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")
	req.Header.Set("Referer", "https://www.tiktok.com/")
	
	// Важно: устанавливаем диапазон байтов для загрузки конкретной части
	if end > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("ошибка при выполнении запроса: %v", err)
	}
	defer resp.Body.Close()

	// Проверяем статус ответа
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("сервер вернул неожиданный статус: %s", resp.Status)
	}

	// Записываем данные в определенное место в файле
	outputSection := &SectionWriter{
		W:    output,
		Base: start,
		Size: end-start+1,
	}
	_, err = io.Copy(outputSection, resp.Body)
	if err != nil {
		return fmt.Errorf("ошибка при сохранении части файла: %v", err)
	}

	return nil
}

func main() {
	// URL видео
	videoURL := "https://v16m-default.akamaized.net/5bebe9f97483095ff1755581443217f4/67bde0ca/video/tos/alisg/tos-alisg-pv-0037/o0Bnb6EwqkAiKiwG2VKk21f990BQAAZEpZnzhu/?a=0&bti=OTg7QGo5QHM6OjZALTAzYCMvcCMxNDNg&ch=0&cr=0&dr=0&er=0&lr=all&net=0&cd=0%7C0%7C0%7C1&cv=1&br=290494&bt=145247&ft=XE5bCqT0m7jPD12OmwMJ3wUYL3yKMeF~O5&mime_type=video_mp4&qs=13&rc=Mzd4eXA5cnc0eDMzODgzNEBpMzd4eXA5cnc0eDMzODgzNEAyZ2BkMmRjaGtgLS1kLzFzYSMyZ2BkMmRjaGtgLS1kLzFzcw%3D%3D&vvpl=1&l=202502250924247C072D8C320AFA0B41F9&btag=e00048000"
	
	// Имя файла, в который будет сохранено видео
	outputFile := "downloaded_video.mp4"
	
	// Известный размер видео в байтах
	expectedSize := int64(631207581)

	// Проверяем поддержку Range запросов
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DisableCompression: true,
		},
	}

	// Отправляем HEAD запрос для проверки поддержки диапазонов
	req, err := http.NewRequest("HEAD", videoURL, nil)
	if err != nil {
		fmt.Printf("Ошибка при создании HEAD запроса: %v\n", err)
		return
	}
	
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Ошибка при выполнении HEAD запроса: %v\n", err)
		return
	}
	resp.Body.Close()

	// Создаем файл для сохранения видео
	out, err := os.Create(outputFile)
	if err != nil {
		fmt.Printf("Ошибка при создании файла: %v\n", err)
		return
	}
	defer out.Close()

	// Предварительно выделяем место для файла
	if err := out.Truncate(expectedSize); err != nil {
		fmt.Printf("Ошибка при выделении места для файла: %v\n", err)
		return
	}

	fmt.Println("Начинаем загрузку видео...")

	startTime := time.Now()

	// Используем параллельную загрузку, если сервер поддерживает Range запросы
	if resp.Header.Get("Accept-Ranges") == "bytes" {
		// Количество параллельных потоков загрузки
		numChunks := 8
		chunkSize := expectedSize / int64(numChunks)
		
		var wg sync.WaitGroup
		
		// Запускаем параллельные загрузки частей файла
		for i := 0; i < numChunks; i++ {
			wg.Add(1)
			
			start := int64(i) * chunkSize
			end := start + chunkSize - 1
			
			// Для последнего куска устанавливаем конец в размер файла
			if i == numChunks-1 {
				end = expectedSize - 1
			}
			
			go downloadChunk(videoURL, start, end, out, &wg)
		}
		
		// Ожидаем завершения всех загрузок
		wg.Wait()
	} else {
		// Если сервер не поддерживает Range запросы, загружаем файл целиком
		fmt.Println("Сервер не поддерживает частичную загрузку, скачиваем файл целиком...")
		
		req, err := http.NewRequest("GET", videoURL, nil)
		if err != nil {
			fmt.Printf("Ошибка при создании запроса: %v\n", err)
			return
		}
		
		// Добавляем заголовки для имитации браузера
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
		req.Header.Set("Accept", "video/mp4,video/*;q=0.9,*/*;q=0.8")
		req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")
		req.Header.Set("Referer", "https://www.tiktok.com/")
		
		// Настраиваем клиент с улучшенными параметрами
		downloadClient := &http.Client{
			Timeout: 30 * time.Minute,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 100,
				IdleConnTimeout:     90 * time.Second,
				DisableCompression:  true,
			},
		}
		
		resp, err := downloadClient.Do(req)
		if err != nil {
			fmt.Printf("Ошибка при выполнении запроса: %v\n", err)
			return
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != http.StatusOK {
			fmt.Printf("Сервер вернул неожиданный статус: %s\n", resp.Status)
			return
		}
		
		_, err = io.Copy(out, resp.Body)
		if err != nil {
			fmt.Printf("Ошибка при сохранении файла: %v\n", err)
			return
		}
	}

	elapsedTime := time.Since(startTime)

	fmt.Printf("\nЗагрузка завершена. Сохранено в файл: %s\n", outputFile)
	
	// Проверяем размер загруженного файла
	fileInfo, err := out.Stat()
	if err != nil {
		fmt.Printf("Ошибка при получении информации о файле: %v\n", err)
		return
	}
	
	fileSize := fileInfo.Size()
	fmt.Printf("Размер файла: %d байт\n", fileSize)
	
	if fileSize != expectedSize {
		fmt.Printf("Внимание: размер загруженного файла (%d) отличается от ожидаемого (%d)\n", fileSize, expectedSize)
	} else {
		fmt.Println("Размер файла соответствует ожидаемому значению.")
	}
	
	// Вычисляем среднюю скорость загрузки
	speedMbps := float64(fileSize) / elapsedTime.Seconds() / 1024 / 1024
	fmt.Printf("Время загрузки: %s\n", elapsedTime)
	fmt.Printf("Средняя скорость: %.2f МБ/с\n", speedMbps)
}
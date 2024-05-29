package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"

	"github.com/gocolly/colly"
)

var __main__ string

var kbDownloaded uint64
var toDownload uint64

type torrentFile struct {
	link    string
	quality string
	seeds   uint32
}

type moviePage string

type movie struct {
	title    string
	coverURL string
	year     string
	movie    moviePage
}

type searchQuery struct {
	title    string
	quality  string
	genre    string
	rating   uint8
	order    string
	year     string
	language string
}

var defaultQuery = searchQuery{
	title:    "0",
	quality:  "all",
	genre:    "all",
	rating:   0,
	order:    "latest",
	year:     "0",
	language: "all",
}

func findMovies(query searchQuery) []movie {
	// Replace placeholders
	if query.title == "" {
		query.title = defaultQuery.title
	}
	if query.quality == "" {
		query.quality = defaultQuery.quality
	}
	if query.genre == "" {
		query.genre = defaultQuery.genre
	}
	if query.rating == 0 {
		query.rating = defaultQuery.rating
	}
	if query.order == "" {
		query.order = defaultQuery.order
	}
	if query.year == "" {
		query.year = defaultQuery.year
	}
	if query.language == "" {
		query.language = defaultQuery.language
	}

	// Build URL
	URL := fmt.Sprintf("https://yts.mx/browse-movies/%s/%s/%s/%d/%s/%s/%s", query.title, query.quality, query.genre, query.rating, query.order, query.year, query.language)

	// Start collecting data
	var movies []movie
	c := colly.NewCollector()

	c.OnHTML("div.row", func(h *colly.HTMLElement) {
		h.ForEach("div.browse-movie-wrap", func(i int, h2 *colly.HTMLElement) {
			// Get inside each movie
			this := movie{}

			// Get title
			h2.ForEach("a.browse-movie-title", func(i int, h3 *colly.HTMLElement) {
				this.title = h3.Text
				this.movie = moviePage(h3.Attr("href"))
			})

			// Get year
			h2.ForEach("div.browse-movie-year", func(i int, h3 *colly.HTMLElement) {
				this.year = h3.Text
			})

			// Get cover
			h2.ForEach("img.img-responsive", func(i int, h3 *colly.HTMLElement) {
				this.coverURL = h3.Attr("src")
			})

			movies = append(movies, this)

		})
	})

	c.OnRequest(func(r *colly.Request) {
		log.Println("Requesting", r.URL)
	})

	c.OnResponse(func(r *colly.Response) {
		log.Println("Response from", r.Request.URL)
	})

	c.Visit(URL)

	return movies
}

func (mv movie) getTorrents() []torrentFile {
	c := colly.NewCollector()

	var torrents []torrentFile

	c.OnHTML("div.row", func(h *colly.HTMLElement) {
		h.ForEach("p.hidden-xs", func(i int, h2 *colly.HTMLElement) {
			h2.ForEach("a", func(i int, h3 *colly.HTMLElement) {
				// Skip if not a movie
				if h3.Attr("rel") != "nofollow" {
					return
				}

				torrents = append(torrents, torrentFile{
					link:    h3.Attr("href"),
					quality: h3.Text,
				})
			})
		})

		processed := 0
		h.ForEach("div.tech-spec-element", func(i int, h2 *colly.HTMLElement) {
			if processed > len(torrents) {
				return
			}

			// Get text
			t := h2.Text
			t = strings.ReplaceAll(t, " ", "")

			// Text too short
			if len(t) < 5 {
				return
			}

			// Check prefix
			if !(strings.HasPrefix(t, "Seeds")) {
				return
			}

			// Trim prefix
			t = strings.TrimPrefix(t, "Seeds")

			// Convert to int
			seeds, err := strconv.Atoi(t)
			if err != nil {
				log.Println(err)
				return
			}

			// Make sure we don;t overwrite anything
			if torrents[processed].seeds != 0 {
				return
			}

			// Set values
			torrents[processed].seeds = uint32(seeds)
			processed += 1

		})
	})

	c.Visit(string(mv.movie))

	return torrents
}

func (tor torrentFile) download() error {
	// Download file
	res, err := http.Get(tor.link)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	// Read file
	f, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}

	// Save to disk
	err = os.WriteFile(path.Join(__main__, "/movies/temp.torrent"), f, os.ModePerm)
	if err != nil {
		return err
	}

	// Download movie
	cmd := exec.Command("npx", "torrent-dl", "--verbose", "--input", "temp.torrent")
	cmd.Dir = path.Join(__main__, "/movies/")
	cmd.Stdin = os.Stdin
	log.Println(cmd.String())

	// Get STDOUT
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	// Start command
	if err := cmd.Start(); err != nil {
		return err
	}

	// Scan for text
	stdoutScanner := bufio.NewScanner(stdout)
	go func() {
		enteredTable := false
		var downloaded uint64
		for stdoutScanner.Scan() {
			t := stdoutScanner.Text()

			// Check if we are inside the table
			if len(t) < 1 {
				continue
			}
			if []rune(t)[0] == '\u251c' {
				enteredTable = true
			} else if []rune(t)[0] == '\u2514' {
				enteredTable = false
				kbDownloaded = downloaded
				downloaded = 0
			}

			// Check if in table or in details
			if !enteredTable || []rune(t)[0] != '\u2502' {
				// Check if in details
				if strings.Contains(t, "KB") || strings.Contains(t, "MB") || strings.Contains(t, "GB") {
					if len(strings.Split(t, " | ")) < 2 {
						continue
					}
					rawtd := strings.Split(strings.Split(t, " | ")[1], " ")

					amount, err := strconv.ParseFloat(strings.ReplaceAll(rawtd[0], "\x1b[32m", ""), 64)
					if err != nil || amount == 0 {
						log.Println(err)
						log.Println(rawtd[0])
						continue
					}

					unit := rawtd[1]

					var multiplier float64
					switch strings.ReplaceAll(unit, "\x1b[39m", "") {
					case "KB":
						multiplier = 1
					case "MB":
						multiplier = 1024
					case "GB":
						multiplier = 1048576
					}

					toDownload = max(toDownload, uint64(multiplier*amount))
				}

				continue
			}

			// Check if source downloaded anything
			if strings.Count(t, "Bytes") > 1 {
				continue
			}

			// Get downloaded amount
			rawDownAmount := strings.Split(strings.Split(t, "'")[3], " ")
			downAmount := rawDownAmount[0]
			downUnit := rawDownAmount[1]

			// Calculate kilobytes
			var multiplier float64
			switch downUnit {
			case "KB":
				multiplier = 1
			case "MB":
				multiplier = 1024
			case "GB":
				multiplier = 1048576
			}

			d, err := strconv.ParseFloat(downAmount, 64)
			if err != nil {
				log.Println(err)
				continue
			}
			downloaded += uint64(d * multiplier)

			log.Println(float64(kbDownloaded) / float64(toDownload) * 100)
		}
	}()

	return cmd.Wait()
}

func main() {
	// Get file directory
	var err error
	__main__, err = os.Getwd()
	if err != nil {
		log.Panicln(err)
	}

	movs := findMovies(searchQuery{rating: 9})

	tor := movs[1].getTorrents()[1]

	tor.download()

}

package main

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/disintegration/imageorient"
	"github.com/lucasb-eyer/go-colorful"
	"github.com/muesli/termenv"
	"github.com/nfnt/resize"
	"golang.org/x/net/html"
	"gopkg.in/yaml.v2"

	"github.com/charmbracelet/bubbles/paginator"
	"github.com/charmbracelet/lipgloss"
	"github.com/kapmahc/epub"

	tea "github.com/charmbracelet/bubbletea"
)

// TODO: file browser, 2 pages view
type Config struct {
	Foreground string `yaml:"foreground"`
	Background string `yaml:"background"`
	TwoPages   bool   `yaml:"two_pages"`
}

type BookSave struct {
	Title  string `yaml:"title"`
	Offset int    `yaml:"offset"`
	Page   int    `yaml:"page"`
}

var (
	duration = time.Second * 3
	interval = time.Millisecond
)

type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(time.Duration(interval), func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func hash(s string) string {
	h := fnv.New32a()
	h.Write([]byte(s))
	return strconv.FormatUint(uint64(h.Sum32()), 10)
}

func readerToImage(width uint, height uint, r io.Reader) (string, error) {
	img, _, err := imageorient.Decode(r)
	if err != nil {
		return "", err
	}

	return imageToString(width, height, img)
}

func imageToString(width, height uint, img image.Image) (string, error) {
	img = resize.Thumbnail(width, height*2-4, img, resize.Lanczos3)
	b := img.Bounds()
	w := b.Max.X
	h := b.Max.Y
	p := termenv.ColorProfile()
	str := strings.Builder{}
	for y := 0; y < h; y += 2 {
		for x := w; x < int(width); x = x + 2 {
			str.WriteString(" ")
		}
		for x := 0; x < w; x++ {
			c1, _ := colorful.MakeColor(img.At(x, y))
			color1 := p.Color(c1.Hex())
			c2, _ := colorful.MakeColor(img.At(x, y+1))
			color2 := p.Color(c2.Hex())
			str.WriteString(termenv.String("▀").
				Foreground(color1).
				Background(color2).
				String())
		}
		str.WriteString("\n")
	}
	return str.String(), nil
}

func newModel(bk *epub.Book) model {
	p := paginator.NewModel()
	p.Type = paginator.Dots
	p.PerPage = 1
	p.ActiveDot = lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "235", Dark: "252"}).
		Background(lipgloss.AdaptiveColor{Light: "#D9DCCF", Dark: "#353533"}).
		Render("•")
	p.InactiveDot = lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "250", Dark: "238"}).
		Background(lipgloss.AdaptiveColor{Light: "#D9DCCF", Dark: "#353533"}).
		Render("•")
	p.UseUpDownKeys = false
	p.UsePgUpPgDownKeys = false
	p.SetTotalPages(len(bk.Opf.Spine.Items))

	v := viewport.Model{}
	v.HighPerformanceRendering = false

	m := model{
		paginator: p,
		items:     bk.Opf.Spine.Items,
		bk:        bk,
		timeout:   time.Now().Add(duration),
		viewport:  v,
	}
	save, err := m.loadPosition()
	if err != nil {
	} else {
		m.paginator.Page = save.Page
		m.viewport.YOffset = save.Offset
	}

	return m
}

type model struct {
	items     []epub.SpineItem
	paginator paginator.Model
	bk        *epub.Book
	image     string
	width     uint
	height    uint
	ready     bool
	startup   bool
	viewport  viewport.Model
	timeout   time.Time
	lastTick  time.Time
}

func (m model) renderPage() string {
	var b strings.Builder
	start, end := m.paginator.GetSliceBounds(len(m.items))
	for _, item := range m.items[start:end] {
		buf := new(bytes.Buffer)
		for _, mItem := range m.bk.Opf.Manifest {
			if mItem.ID == item.IDref {
				f, _ := m.bk.Open(mItem.Href)
				buf.ReadFrom(f)
			}
		}
		// Parse html
		tokenizer := html.NewTokenizer(buf)
		textTags := []string{
			"a",
			"p", "span", "em", "string", "blockquote", "q", "cite",
			"h1", "h2", "h3", "h4", "h5", "h6", "img",
		}
		enter := false
		for {
			tt := tokenizer.Next()
			t := tokenizer.Token()

			err := tokenizer.Err()
			if err == io.EOF {
				break
			}

			switch tt {
			case html.ErrorToken:
				b.WriteString("end")
			case html.StartTagToken, html.SelfClosingTagToken:
				enter = false

				// Print image
				if "img" == t.Data {
					img := new(bytes.Buffer)
					for _, attr := range t.Attr {
						if attr.Key == "src" {
							i, _ := m.bk.Open(strings.Replace(attr.Val, "../", "", -1))
							img.ReadFrom(i)
							s, _ := readerToImage(m.width, m.height, img)
							b.WriteString(s)
						}

					}
				}
				// Filter non text tags
				for _, ttt := range textTags {
					if t.Data == ttt {
						enter = true
						break
					}
				}
			case html.TextToken:
				data := strings.TrimSpace(t.String())
				if enter {
					if len(data) > 0 {
						b.WriteString(data + "\n")
					}
				}
			}

		}
	}
	var str = lipgloss.NewStyle().
		Width(int(m.width)).
		Background(lipgloss.Color("grey")).
		Foreground(lipgloss.Color("white")).
		//Padding(1, 2, 1).
		Render(b.String())
	//twoPage := lipgloss.NewStyle().Width(int(m.width)/2-2).Height(int(m.height)).Render(str)
	return str

}

func (m model) savePosition() {
	configHome := os.ExpandEnv("$HOME/.termepub")
	if _, err := os.Stat(configHome); os.IsNotExist(err) {
		if err := os.Mkdir(configHome, os.ModePerm); err != nil {
			log.Fatal("cannot create config dir:", err)
		}
	}
	configType := "yml"
	configName := m.bk.Opf.Metadata.Title[0]
	name := hash(configName)
	configPath := filepath.Join(configHome, name+"."+configType)
	save := BookSave{
		Title:  configName,
		Offset: m.viewport.YOffset,
		Page:   m.paginator.Page,
	}
	data, err := yaml.Marshal(&save)
	if err != nil {
		log.Fatal("", err)
	}
	err = os.WriteFile(configPath, data, 0644)
	if err != nil {
		log.Fatal("cannot write file:", err)
	}

}

func (m model) loadPosition() (save BookSave, err error) {
	configHome := os.ExpandEnv("$HOME/.termepub")
	configType := "yml"
	configName := m.bk.Opf.Metadata.Title[0]
	name := hash(configName)
	configPath := filepath.Join(configHome, name+"."+configType)
	if _, err = os.Stat(configPath); os.IsNotExist(err) {
		return
	}
	buf, err := ioutil.ReadFile(configPath)
	if err != nil {
		return
	}

	err = yaml.Unmarshal(buf, &save)
	if err != nil {
		return
	}

	return
}

func (m model) Init() tea.Cmd {
	return tick()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = uint(msg.Width)
		m.height = uint(msg.Height)
		if !m.ready {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - 1
			m.viewport.SetContent(m.renderPage())
			m.ready = true
			//m.viewport.YOffset = m.offset
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height
		}
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "right":
			m.paginator.NextPage()
			m.viewport.YOffset = 0
			m.viewport.SetContent(m.renderPage())
		case "left":
			m.paginator.PrevPage()
			m.viewport.YOffset = 0
			m.viewport.SetContent(m.renderPage())
		}
	case tickMsg:
		t := time.Time(msg)
		if t.After(m.timeout) {
			m.startup = true
			return m, nil
		}
		m.lastTick = t
		return m, tick()
	}
	m.viewport, cmd = m.viewport.Update(msg)
	m.savePosition()
	return m, cmd
}

func (m model) View() string {
	if !m.ready {
		return "\n  Initializing..."
	}
	if !m.startup {
		subtle := lipgloss.AdaptiveColor{Light: "#D9DCCF", Dark: "#383838"}
		dialogBoxStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#874BFD")).
			Padding(1, 0).
			BorderTop(true).
			BorderLeft(true).
			BorderRight(true).
			BorderBottom(true)
		text := fmt.Sprintf(
			"%s%s%s\n%s%s",
			strings.Repeat(" ", 5),
			m.bk.Opf.Metadata.Title[0],
			strings.Repeat(" ", 5),
			strings.Repeat(" ", 5),
			m.bk.Opf.Metadata.Creator[0].Data,
		)
		landing := lipgloss.Place(int(m.width), int(m.height),
			lipgloss.Center, lipgloss.Center,
			dialogBoxStyle.Render(text),
			lipgloss.WithWhitespaceChars("⠿"),
			lipgloss.WithWhitespaceForeground(subtle),
		)
		return landing
	}
	statusBarStyle := lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#343433", Dark: "#C1C6B2"}).
		Background(lipgloss.AdaptiveColor{Light: "#D9DCCF", Dark: "#353533"})
	title := lipgloss.NewStyle().
		Inherit(statusBarStyle).
		Width(int(m.width) / 3).
		Align(lipgloss.Left).
		Render(" " + m.bk.Opf.Metadata.Title[0])
	pageProgress := lipgloss.NewStyle().
		Inherit(statusBarStyle).
		Width(int(m.width) / 3).
		Align(lipgloss.Center).
		Render(m.paginator.View())
	scroll := lipgloss.NewStyle().
		Inherit(statusBarStyle).
		Width(int(m.width) / 3).
		Align(lipgloss.Right).
		Render(fmt.Sprintf("%3.f%%", m.viewport.ScrollPercent()*100))

	bar := lipgloss.JoinHorizontal(
		lipgloss.Bottom,
		title,
		pageProgress,
		scroll,
	)

	return m.viewport.View() + "\n" + bar
}

func main() {
	bk, err := epub.Open(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer bk.Close()
	p := tea.NewProgram(newModel(bk))
	p.EnterAltScreen()
	p.EnableMouseCellMotion()
	defer p.ExitAltScreen()
	if err := p.Start(); err != nil {
		log.Fatal(err)
	}
}

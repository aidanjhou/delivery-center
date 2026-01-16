package main

import (
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v2"
)

// Config holds all the literal strings used in the application
type Config struct {
	Site       SiteConfig       `json:"site"`
	Navigation NavigationConfig `json:"navigation"`
	Home       HomeConfig       `json:"home"`
	Apps       AppsConfig       `json:"apps"`
	Detail     DetailConfig     `json:"detail"`
}

type SiteConfig struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
}

type NavigationConfig struct {
	Home     string `json:"home"`
	Apps     string `json:"apps"`
	BackHome string `json:"back_home"`
	BackApps string `json:"back_apps"`
}

type HomeConfig struct {
	Title       string `json:"title"`
	Subtitle    string `json:"subtitle"`
	Header      string `json:"header"`
	ViewAll     string `json:"view_all"`
	ViewAllLink string `json:"view_all_link"`
}

type AppsConfig struct {
	Title         string `json:"title"`
	Subtitle      string `json:"subtitle"`
	BackHome      string `json:"back_home"`
	SearchPlace   string `json:"search_placeholder"`
	ResultsAll    string `json:"results_all"`
	ResultsSearch string `json:"results_search"`
}

type DetailConfig struct {
	Title    string       `json:"title"`
	Subtitle string       `json:"subtitle"`
	BackApps string       `json:"back_apps"`
	Download string       `json:"download"`
	Labels   DetailLabels `json:"labels"`
}

type DetailLabels struct {
	Version   string `json:"version"`
	Developer string `json:"developer"`
	Rating    string `json:"rating"`
	Downloads string `json:"downloads"`
	Price     string `json:"price"`
	Updated   string `json:"updated"`
}

// appConfig holds all the configuration strings
var appConfig = Config{
	Site: SiteConfig{
		Title:       "交付中心",
		Description: "浏览最新交付的分发包",
		Icon:        "🚚",
	},
	Navigation: NavigationConfig{
		Home:     "首页",
		Apps:     "全部分发包",
		BackHome: "← 返回首页",
		BackApps: "← 返回分发包列表",
	},
	Home: HomeConfig{
		Title:       "交付中心 - 首页",
		Subtitle:    "浏览最新交付的分发包",
		Header:      "最新交付的分发包",
		ViewAll:     "查看全部分发包 →",
		ViewAllLink: "/apps",
	},
	Apps: AppsConfig{
		Title:         "交付中心 - 分发包列表",
		Subtitle:      "浏览全部分发包",
		BackHome:      "← 返回首页",
		SearchPlace:   "搜索分发包，过滤条件可以是 name, category, producer...",
		ResultsAll:    "共 %d 个分发包",
		ResultsSearch: "找到 %d 个结果，过滤条件是：\"%s\"",
	},
	Detail: DetailConfig{
		Title:    "交付中心 - %s",
		Subtitle: "查看分发包详情",
		BackApps: "← 返回分发包列表",
		Download: "下载",
		Labels: DetailLabels{
			Version:   "版本",
			Developer: "负责人",
			Rating:    "评分",
			Downloads: "下载次数",
			Price:     "价格",
			Updated:   "更新时间",
		},
	},
}

type App struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Category     string    `json:"category"`
	Description  string    `json:"description"`
	Version      string    `json:"version"`
	Developer    string    `json:"developer"`
	Rating       float64   `json:"rating"`
	Downloads    int       `json:"downloads"`
	Price        string    `json:"price"`
	Icon         string    `json:"icon"`
	DownloadLink string    `json:"download_link"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

var apps []App
var templates *template.Template
var appsMutex sync.RWMutex

type AppWatcher struct {
	watcher   *fsnotify.Watcher
	watchPath string
	stopChan  chan bool
	ticker    *time.Ticker
}

type PackageMetadata struct {
	Name         string    `json:"name"`
	Category     string    `json:"category"`
	Description  string    `json:"description"`
	Version      string    `json:"version"`
	Developer    string    `json:"developer"`
	Rating       float64   `json:"rating"`
	Downloads    int       `json:"downloads"`
	Price        string    `json:"price"`
	Icon         string    `json:"icon"`
	UpdateTime   time.Time `json:"update_time"`
	DownloadLink string    `json:"download_link"`
}

func NewAppWatcher(watchPath string) (*AppWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	return &AppWatcher{
		watcher:   watcher,
		watchPath: watchPath,
		stopChan:  make(chan bool),
	}, nil
}

func (aw *AppWatcher) Start() error {
	if err := aw.watcher.Add(aw.watchPath); err != nil {
		return err
	}

	aw.ticker = time.NewTicker(60 * time.Second)

	go aw.watchLoop()
	aw.scanExistingPackages()
	return nil
}

func (aw *AppWatcher) Stop() {
	aw.stopChan <- true
	if aw.ticker != nil {
		aw.ticker.Stop()
	}
	aw.watcher.Close()
}

func (aw *AppWatcher) watchLoop() {
	for {
		select {
		case event, ok := <-aw.watcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Create == fsnotify.Create || event.Op&fsnotify.Write == fsnotify.Write {
				aw.processPackage(event.Name)
			}
		case err, ok := <-aw.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("Watcher error: %v", err)
		case <-aw.ticker.C:
			aw.scanExistingPackages()
			log.Printf("Periodic scan completed")
		case <-aw.stopChan:
			return
		}
	}
}

func (aw *AppWatcher) scanExistingPackages() {
	filepath.WalkDir(aw.watchPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && aw.isPackageFile(path) {
			aw.processPackage(path)
		}
		return nil
	})
}

func (aw *AppWatcher) isPackageFile(filename string) bool {
	return filepath.Base(filename) == "README.md"
}

func (aw *AppWatcher) processPackage(readmePath string) {
	dirPath := filepath.Dir(readmePath)
	dirName := filepath.Base(dirPath)

	metadata, err := aw.extractMetadata(readmePath)
	if err != nil {
		log.Printf("Failed to extract metadata from %s: %v", readmePath, err)
		return
	}

	app := App{
		ID:           dirName,
		Name:         metadata.Name,
		Category:     metadata.Category,
		Description:  metadata.Description,
		Version:      metadata.Version,
		Developer:    metadata.Developer,
		Rating:       metadata.Rating,
		Downloads:    metadata.Downloads,
		Price:        metadata.Price,
		Icon:         metadata.Icon,
		DownloadLink: metadata.DownloadLink,
		CreatedAt:    time.Now(),
		UpdatedAt:    metadata.UpdateTime,
	}

	appsMutex.Lock()
	defer appsMutex.Unlock()

	for i, existingApp := range apps {
		if existingApp.ID == app.ID {
			apps[i] = app
			log.Printf("Updated app: %s", app.Name)
			return
		}
	}

	apps = append(apps, app)
	log.Printf("Added new app: %s", app.Name)
}

func (aw *AppWatcher) extractMetadata(filePath string) (PackageMetadata, error) {
	return aw.parseReadmeMetadata(filePath)
}

func (aw *AppWatcher) parseReadmeMetadata(readmePath string) (PackageMetadata, error) {
	content, err := os.ReadFile(readmePath)
	if err != nil {
		return PackageMetadata{}, err
	}

	contentStr := string(content)
	metadata := PackageMetadata{
		Name:         "Unknown",
		Category:     "Unknown",
		Description:  "No description available",
		Version:      "1.0.0",
		Developer:    "Unknown",
		Rating:       10.0,
		Downloads:    0,
		Price:        "Free",
		Icon:         "📦",
		UpdateTime:   time.Now(),
		DownloadLink: "",
	}

	if strings.Contains(contentStr, "---") {
		parts := strings.SplitN(contentStr, "---", 3)
		if len(parts) >= 3 {
			frontMatter := parts[1]
			description := parts[2]

			metadata.Rating = 10.0

			var frontMatterData map[string]interface{}
			if err := yaml.Unmarshal([]byte(frontMatter), &frontMatterData); err == nil {
				if title, ok := frontMatterData["title"].(string); ok {
					metadata.Name = title
				}
				if category, ok := frontMatterData["category"]; ok {
					if categorySlice, ok := category.([]interface{}); ok && len(categorySlice) > 0 {
						if firstCat, ok := categorySlice[0].(string); ok {
							metadata.Category = firstCat
						}
					}
				}
				if update_time, ok := frontMatterData["update_time"].(string); ok {
					if updateTime, err := time.Parse(time.RFC3339, update_time); err == nil {
						metadata.UpdateTime = updateTime
					}
				}
				if producer, ok := frontMatterData["producer"]; ok {
					if producerSlice, ok := producer.([]interface{}); ok {
						var producers []string
						for _, p := range producerSlice {
							if producerStr, ok := p.(string); ok {
								producers = append(producers, producerStr)
							}
						}
						metadata.Developer = strings.Join(producers, ", ")
					}
				}
				if downloadLink, ok := frontMatterData["download_link"]; ok {
					if downloadLinkSlice, ok := downloadLink.([]interface{}); ok && len(downloadLinkSlice) > 0 {
						if firstLink, ok := downloadLinkSlice[0].(string); ok {
							metadata.DownloadLink = firstLink
						}
					} else if downloadLinkStr, ok := downloadLink.(string); ok {
						metadata.DownloadLink = downloadLinkStr
					}
				}
			}

			descLines := strings.Split(description, "\n")
			for _, line := range descLines {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "<!-- description -->") {
					continue
				}
				if line != "" && !strings.HasPrefix(line, "<") {
					metadata.Description = line
					break
				}
			}

			if metadata.Name == "Unknown" {
				dirName := filepath.Base(filepath.Dir(readmePath))
				metadata.Name = dirName
			}
		}
	}

	return metadata, nil
}

func init() {
	var err error

	funcMap := template.FuncMap{
		"hasPrefix": strings.HasPrefix,
	}

	templates, err = template.New("").Funcs(funcMap).ParseGlob("templates/*.html")
	if err != nil {
		log.Fatal("Error parsing templates:", err)
	}
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	appsMutex.RLock()
	latestApps := make([]App, len(apps))
	copy(latestApps, apps)
	appsMutex.RUnlock()

	if len(latestApps) > 10 {
		latestApps = latestApps[:10]
	}

	data := struct {
		Config Config
		Apps   []App
	}{
		Config: appConfig,
		Apps:   latestApps,
	}

	renderTemplate(w, "home.html", data)
}

func appListHandler(w http.ResponseWriter, r *http.Request) {
	searchQuery := r.URL.Query().Get("search")

	appsMutex.RLock()
	allApps := make([]App, len(apps))
	copy(allApps, apps)
	appsMutex.RUnlock()

	filteredApps := allApps

	if searchQuery != "" {
		searchQuery = strings.ToLower(searchQuery)
		filteredApps = []App{}
		for _, app := range allApps {
			if strings.Contains(strings.ToLower(app.Name), searchQuery) ||
				strings.Contains(strings.ToLower(app.Category), searchQuery) ||
				strings.Contains(strings.ToLower(app.Description), searchQuery) ||
				strings.Contains(strings.ToLower(app.Developer), searchQuery) {
				filteredApps = append(filteredApps, app)
			}
		}
	}

	data := struct {
		Config      Config
		Apps        []App
		SearchQuery string
		TotalCount  int
	}{
		Config:      appConfig,
		Apps:        filteredApps,
		SearchQuery: searchQuery,
		TotalCount:  len(filteredApps),
	}

	renderTemplate(w, "apps.html", data)
}

func appDetailHandler(w http.ResponseWriter, r *http.Request) {
	appID := r.URL.Path[len("/app/"):]

	appsMutex.RLock()
	var app App
	found := false
	for _, a := range apps {
		if a.ID == appID {
			app = a
			found = true
			break
		}
	}
	appsMutex.RUnlock()

	if !found {
		http.NotFound(w, r)
		return
	}

	var downloadsFormatted string
	if app.Downloads > 0 {
		downloadsFormatted = fmt.Sprintf("%.1fM", float64(app.Downloads)/1000000.0)
	} else {
		downloadsFormatted = "暂无"
	}

	data := struct {
		Config             Config
		Title              string
		App                App
		DownloadsFormatted string
	}{
		Config:             appConfig,
		Title:              fmt.Sprintf(appConfig.Detail.Title, app.Name),
		App:                app,
		DownloadsFormatted: downloadsFormatted,
	}

	renderTemplate(w, "detail.html", data)
}

func downloadHandler(w http.ResponseWriter, r *http.Request) {
	appID := r.URL.Path[len("/download/"):]

	appsMutex.RLock()
	var app App
	found := false
	for _, a := range apps {
		if a.ID == appID {
			app = a
			found = true
			break
		}
	}
	appsMutex.RUnlock()

	if !found {
		http.NotFound(w, r)
		return
	}

	if app.DownloadLink == "" {
		http.Error(w, "No download link available", http.StatusNotFound)
		return
	}

	// If it's an external URL (http/https), redirect directly
	if strings.HasPrefix(app.DownloadLink, "http://") ||
		strings.HasPrefix(app.DownloadLink, "https://") {
		http.Redirect(w, r, app.DownloadLink, http.StatusMovedPermanently)
		return
	}

	// Otherwise, treat as relative file path and proxy the download
	// Always prepend with packages/appID/
	filePath := filepath.Join("packages", appID, app.DownloadLink)
	http.ServeFile(w, r, filePath)
}

func renderTemplate(w http.ResponseWriter, templateName string, data interface{}) {
	err := templates.ExecuteTemplate(w, templateName, data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func main() {
	watchPath := "./packages"
	if err := os.MkdirAll(watchPath, 0755); err != nil {
		log.Fatal("Failed to create watch directory:", err)
	}

	watcher, err := NewAppWatcher(watchPath)
	if err != nil {
		log.Fatal("Failed to create watcher:", err)
	}

	if err := watcher.Start(); err != nil {
		log.Fatal("Failed to start watcher:", err)
	}
	defer watcher.Stop()

	http.HandleFunc("/", homeHandler)
	http.HandleFunc("/apps", appListHandler)
	http.HandleFunc("/app/", appDetailHandler)
	http.HandleFunc("/download/", downloadHandler)

	port := ":8080"
	fmt.Printf("Server starting on port %s...\n", port)
	fmt.Printf("Home: http://localhost%s\n", port)
	fmt.Printf("Apps: http://localhost%s/apps\n", port)
	fmt.Printf("Watching packages directory: %s\n", watchPath)

	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatal("Server error:", err)
	}
}

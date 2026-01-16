package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strings"
	"time"
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
		SearchPlace:   "搜索分发包，过滤条件可以是 name, category, provider...",
		ResultsAll:    "共 %d 个分发包",
		ResultsSearch: "找到 %d 个结果，过滤条件是：\"%s\"",
	},
	Detail: DetailConfig{
		Title:    "交付中心 - %s",
		Subtitle: "查看分发包详情",
		BackApps: "← 返回分发包列表",
		Download: "下载",
		Labels: DetailLabels{
			Version:   "Version",
			Developer: "Provider",
			Rating:    "Rating",
			Downloads: "Bookings",
			Price:     "Price",
			Updated:   "Updated",
		},
	},
}

type App struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Category    string    `json:"category"`
	Description string    `json:"description"`
	Version     string    `json:"version"`
	Developer   string    `json:"developer"`
	Rating      float64   `json:"rating"`
	Downloads   int       `json:"downloads"`
	Price       string    `json:"price"`
	Icon        string    `json:"icon"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

var apps []App
var templates *template.Template

func init() {
	apps = []App{
		{
			ID:          "1",
			Name:        "Photo Editor Pro",
			Category:    "Photography",
			Description: "Professional photo editing app with filters, effects, and advanced tools.",
			Version:     "3.2.1",
			Developer:   "Pixel Studios",
			Rating:      4.5,
			Downloads:   1500000,
			Price:       "$4.99",
			Icon:        "📷",
			CreatedAt:   time.Now().Add(-30 * 24 * time.Hour),
			UpdatedAt:   time.Now().Add(-1 * 24 * time.Hour),
		},
		{
			ID:          "2",
			Name:        "Task Manager",
			Category:    "Productivity",
			Description: "Organize your tasks and projects with this powerful task management app.",
			Version:     "2.1.0",
			Developer:   "Productive Apps Inc",
			Rating:      4.8,
			Downloads:   2300000,
			Price:       "Free",
			Icon:        "✅",
			CreatedAt:   time.Now().Add(-25 * 24 * time.Hour),
			UpdatedAt:   time.Now().Add(-2 * 24 * time.Hour),
		},
		{
			ID:          "3",
			Name:        "Music Stream",
			Category:    "Music",
			Description: "Stream millions of songs and discover new music with personalized recommendations.",
			Version:     "5.4.2",
			Developer:   "AudioTech",
			Rating:      4.6,
			Downloads:   5600000,
			Price:       "$9.99/month",
			Icon:        "🎵",
			CreatedAt:   time.Now().Add(-20 * 24 * time.Hour),
			UpdatedAt:   time.Now().Add(-3 * 24 * time.Hour),
		},
		{
			ID:          "4",
			Name:        "Fitness Tracker",
			Category:    "Health",
			Description: "Track your workouts, nutrition, and health goals with comprehensive analytics.",
			Version:     "4.1.3",
			Developer:   "HealthTech Solutions",
			Rating:      4.7,
			Downloads:   3200000,
			Price:       "$2.99",
			Icon:        "💪",
			CreatedAt:   time.Now().Add(-18 * 24 * time.Hour),
			UpdatedAt:   time.Now().Add(-4 * 24 * time.Hour),
		},
		{
			ID:          "5",
			Name:        "Weather Now",
			Category:    "Weather",
			Description: "Accurate weather forecasts with radar, alerts, and detailed meteorological data.",
			Version:     "6.0.1",
			Developer:   "WeatherApps Co",
			Rating:      4.4,
			Downloads:   8900000,
			Price:       "Free",
			Icon:        "🌤️",
			CreatedAt:   time.Now().Add(-15 * 24 * time.Hour),
			UpdatedAt:   time.Now().Add(-5 * 24 * time.Hour),
		},
		{
			ID:          "6",
			Name:        "Social Connect",
			Category:    "Social",
			Description: "Connect with friends and share moments in this innovative social platform.",
			Version:     "3.5.0",
			Developer:   "SocialMedia Inc",
			Rating:      4.2,
			Downloads:   4500000,
			Price:       "Free",
			Icon:        "👥",
			CreatedAt:   time.Now().Add(-12 * 24 * time.Hour),
			UpdatedAt:   time.Now().Add(-6 * 24 * time.Hour),
		},
		{
			ID:          "7",
			Name:        "Banking Secure",
			Category:    "Finance",
			Description: "Manage your finances with secure banking, transfers, and investment tracking.",
			Version:     "7.2.1",
			Developer:   "FinTech Pro",
			Rating:      4.9,
			Downloads:   6700000,
			Price:       "Free",
			Icon:        "💰",
			CreatedAt:   time.Now().Add(-10 * 24 * time.Hour),
			UpdatedAt:   time.Now().Add(-7 * 24 * time.Hour),
		},
		{
			ID:          "8",
			Name:        "Game Center",
			Category:    "Games",
			Description: "Play hundreds of games in one app with new games added every week.",
			Version:     "2.8.4",
			Developer:   "GameStudio",
			Rating:      4.3,
			Downloads:   12000000,
			Price:       "Free",
			Icon:        "🎮",
			CreatedAt:   time.Now().Add(-8 * 24 * time.Hour),
			UpdatedAt:   time.Now().Add(-8 * 24 * time.Hour),
		},
		{
			ID:          "9",
			Name:        "News Reader",
			Category:    "News",
			Description: "Stay informed with news from trusted sources around the world.",
			Version:     "4.0.2",
			Developer:   "NewsTech",
			Rating:      4.5,
			Downloads:   3400000,
			Price:       "$1.99",
			Icon:        "📰",
			CreatedAt:   time.Now().Add(-6 * 24 * time.Hour),
			UpdatedAt:   time.Now().Add(-9 * 24 * time.Hour),
		},
		{
			ID:          "10",
			Name:        "Video Player",
			Category:    "Multimedia",
			Description: "Play all video formats with advanced features and subtitle support.",
			Version:     "5.1.0",
			Developer:   "MediaTech",
			Rating:      4.6,
			Downloads:   7800000,
			Price:       "Free",
			Icon:        "🎬",
			CreatedAt:   time.Now().Add(-4 * 24 * time.Hour),
			UpdatedAt:   time.Now().Add(-10 * 24 * time.Hour),
		},
		{
			ID:          "11",
			Name:        "Calculator Plus",
			Category:    "Utilities",
			Description: "Advanced calculator with scientific functions and unit conversions.",
			Version:     "1.5.3",
			Developer:   "Utility Apps",
			Rating:      4.7,
			Downloads:   2100000,
			Price:       "Free",
			Icon:        "🧮",
			CreatedAt:   time.Now().Add(-3 * 24 * time.Hour),
			UpdatedAt:   time.Now().Add(-11 * 24 * time.Hour),
		},
		{
			ID:          "12",
			Name:        "Travel Planner",
			Category:    "Travel",
			Description: "Plan your trips with itinerary management, booking, and travel guides.",
			Version:     "3.0.1",
			Developer:   "TravelTech Solutions",
			Rating:      4.8,
			Downloads:   1900000,
			Price:       "$3.99",
			Icon:        "✈️",
			CreatedAt:   time.Now().Add(-2 * 24 * time.Hour),
			UpdatedAt:   time.Now().Add(-12 * 24 * time.Hour),
		},
	}

	var err error
	templates, err = template.ParseGlob("templates/*.html")
	if err != nil {
		log.Fatal("Error parsing templates:", err)
	}
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	latestApps := apps
	if len(latestApps) > 10 {
		latestApps = apps[:10]
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
	filteredApps := apps

	if searchQuery != "" {
		searchQuery = strings.ToLower(searchQuery)
		filteredApps = []App{}
		for _, app := range apps {
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

	var app App
	found := false
	for _, a := range apps {
		if a.ID == appID {
			app = a
			found = true
			break
		}
	}

	if !found {
		http.NotFound(w, r)
		return
	}

	downloadsFormatted := fmt.Sprintf("%.1fM", float64(app.Downloads)/1000000.0)

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

func renderTemplate(w http.ResponseWriter, templateName string, data interface{}) {
	err := templates.ExecuteTemplate(w, templateName, data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func main() {
	http.HandleFunc("/", homeHandler)
	http.HandleFunc("/apps", appListHandler)
	http.HandleFunc("/app/", appDetailHandler)

	port := ":8080"
	fmt.Printf("Server starting on port %s...\n", port)
	fmt.Printf("Home: http://localhost%s\n", port)
	fmt.Printf("Apps: http://localhost%s/apps\n", port)

	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatal("Server error:", err)
	}
}

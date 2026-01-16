package main

import (
	"database/sql"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/hirochachacha/go-smb2"
	"github.com/joho/godotenv"
	"gopkg.in/yaml.v2"
	_ "modernc.org/sqlite"
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

var templates *template.Template
var globalSMBManager *SMBManager
var db *sql.DB

type SMBShare struct {
	Host     string
	Share    string
	Path     string
	Username string
	Password string
	Domain   string
}

type SMBConnection struct {
	Session *smb2.Session
	Share   *smb2.Share
}

type SMBManager struct {
	connections map[string]*SMBConnection
	mutex       sync.RWMutex
}

func NewSMBManager() *SMBManager {
	return &SMBManager{
		connections: make(map[string]*SMBConnection),
	}
}

func (sm *SMBManager) Connect(shareInfo SMBShare) error {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	shareKey := fmt.Sprintf("%s/%s", shareInfo.Host, shareInfo.Share)

	// Close existing connection if any
	if existingConn, exists := sm.connections[shareKey]; exists {
		existingConn.Share.Umount()
		existingConn.Session.Logoff()
		delete(sm.connections, shareKey)
	}

	conn, err := net.Dial("tcp", fmt.Sprintf("%s:445", shareInfo.Host))
	if err != nil {
		return fmt.Errorf("failed to connect to SMB host %s: %v", shareInfo.Host, err)
	}

	dialer := &smb2.Dialer{}

	if shareInfo.Username != "" {
		// Authenticated access
		dialer.Initiator = &smb2.NTLMInitiator{
			User:     shareInfo.Username,
			Password: shareInfo.Password,
		}
		if shareInfo.Domain != "" {
			dialer.Initiator.(*smb2.NTLMInitiator).Domain = shareInfo.Domain
		}
	} else {
		// Anonymous access - use guest account
		dialer.Initiator = &smb2.NTLMInitiator{
			User:     "guest",
			Password: "",
		}
	}

	session, err := dialer.Dial(conn)
	if err != nil {
		conn.Close()
		if shareInfo.Username == "" {
			return fmt.Errorf("failed to establish anonymous SMB session with %s: %v", shareInfo.Host, err)
		}
		return fmt.Errorf("failed to establish authenticated SMB session with %s: %v", shareInfo.Host, err)
	}

	share, err := session.Mount(shareInfo.Share)
	if err != nil {
		session.Logoff()
		conn.Close()
		return fmt.Errorf("failed to mount SMB share %s: %v", shareInfo.Share, err)
	}

	sm.connections[shareKey] = &SMBConnection{
		Session: session,
		Share:   share,
	}

	if shareInfo.Username == "" {
		log.Printf("Connected via guest account to SMB share: %s", shareKey)
	} else {
		log.Printf("Connected with credentials to SMB share: %s (user: %s)", shareKey, shareInfo.Username)
	}
	return nil
}

func (sm *SMBManager) GetConnection(shareKey string) (*SMBConnection, error) {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()

	conn, exists := sm.connections[shareKey]
	if !exists {
		return nil, fmt.Errorf("SMB connection not found: %s", shareKey)
	}
	return conn, nil
}

func (sm *SMBManager) CloseAll() {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	for _, conn := range sm.connections {
		conn.Share.Umount()
		conn.Session.Logoff()
	}
	sm.connections = make(map[string]*SMBConnection)
}

func parseSMBPath(path string) (*SMBShare, bool) {
	// Parse UNC path like \\192.168.1.151\share\folder
	re := regexp.MustCompile(`^\\\\([^\\]+)\\([^\\]+)(?:\\(.*))?$`)
	matches := re.FindStringSubmatch(path)
	if len(matches) < 3 {
		return nil, false
	}

	shareInfo := &SMBShare{
		Host:  matches[1],
		Share: matches[2],
		Path:  "",
	}

	if len(matches) > 3 {
		shareInfo.Path = matches[3]
	}

	// Get credentials from environment variables
	host := strings.ReplaceAll(shareInfo.Host, ".", "_")
	shareInfo.Username = os.Getenv(fmt.Sprintf("SMB_USER_%s", host))
	shareInfo.Password = os.Getenv(fmt.Sprintf("SMB_PASS_%s", host))
	shareInfo.Domain = os.Getenv(fmt.Sprintf("SMB_DOMAIN_%s", host))

	return shareInfo, true
}

type AppWatcher struct {
	watcher    *fsnotify.Watcher
	watchPaths []string
	stopChan   chan bool
	ticker     *time.Ticker
	smbManager *SMBManager
	smbPaths   map[string]SMBShare
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

func NewAppWatcher(watchPaths []string) (*AppWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	smbManager := NewSMBManager()
	smbPaths := make(map[string]SMBShare)

	// Identify and connect to SMB shares
	localPaths := make([]string, 0)
	for _, path := range watchPaths {
		if shareInfo, isSMB := parseSMBPath(path); isSMB {
			shareKey := fmt.Sprintf("%s/%s", shareInfo.Host, shareInfo.Share)
			smbPaths[shareKey] = *shareInfo
			if err := smbManager.Connect(*shareInfo); err != nil {
				log.Printf("Failed to connect to SMB share %s: %v", shareKey, err)
			} else {
				log.Printf("Connected to SMB share: %s", shareKey)
			}
		} else {
			localPaths = append(localPaths, path)
		}
	}

	return &AppWatcher{
		watcher:    watcher,
		watchPaths: localPaths,
		stopChan:   make(chan bool),
		smbManager: smbManager,
		smbPaths:   smbPaths,
	}, nil
}

func (aw *AppWatcher) Start() error {
	for _, path := range aw.watchPaths {
		if err := aw.watcher.Add(path); err != nil {
			return err
		}
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
			aw.scanSMBShares()
			log.Printf("Periodic scan completed")
		case <-aw.stopChan:
			return
		}
	}
}

func (aw *AppWatcher) scanExistingPackages() {
	// Scan local paths
	for _, watchPath := range aw.watchPaths {
		filepath.WalkDir(watchPath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && aw.isPackageFile(path) {
				aw.processPackage(path)
			}
			return nil
		})
	}

	// Scan SMB paths
	for shareKey, shareInfo := range aw.smbPaths {
		aw.scanSMBShare(shareKey, shareInfo)
	}
}

func (aw *AppWatcher) isPackageFile(filename string) bool {
	return filepath.Base(filename) == "README.md"
}

func (aw *AppWatcher) scanSMBShare(shareKey string, shareInfo SMBShare) {
	conn, err := aw.smbManager.GetConnection(shareKey)
	if err != nil {
		log.Printf("Failed to get SMB connection for %s: %v", shareKey, err)
		return
	}

	basePath := shareInfo.Path
	if basePath == "" {
		basePath = "."
	}

	err = aw.walkSMBDirectory(conn.Share, basePath, shareKey)
	if err != nil {
		log.Printf("Failed to walk SMB directory %s: %v", basePath, err)
	}
}

func (aw *AppWatcher) walkSMBDirectory(share *smb2.Share, path string, shareKey string) error {
	dir, err := share.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()

	entries, err := dir.Readdir(-1)
	if err != nil {
		return err
	}

	log.Printf("SMB Directory %s contents:", path)
	for _, entry := range entries {
		if entry.IsDir() {
			log.Printf("  DIR:  %s", entry.Name())
		} else {
			log.Printf("  FILE: %s", entry.Name())
		}
	}

	for _, entry := range entries {
		// Use forward slashes for SMB paths
		fullPath := strings.Join([]string{path, entry.Name()}, "/")

		if entry.IsDir() {
			err = aw.walkSMBDirectory(share, fullPath, shareKey)
			if err != nil {
				log.Printf("Error walking SMB directory %s: %v", fullPath, err)
			}
		} else if aw.isPackageFile(entry.Name()) {
			log.Printf("Found SMB package: %s", fullPath)
			aw.processSMBPackage(fullPath, shareKey)
		}
	}

	return nil
}

func (aw *AppWatcher) scanSMBShares() {
	for shareKey, shareInfo := range aw.smbPaths {
		// Try to reconnect if connection is lost
		if _, err := aw.smbManager.GetConnection(shareKey); err != nil {
			if err := aw.smbManager.Connect(shareInfo); err != nil {
				log.Printf("Failed to reconnect to SMB share %s: %v", shareKey, err)
				continue
			}
			log.Printf("Reconnected to SMB share: %s", shareKey)
		}

		aw.scanSMBShare(shareKey, shareInfo)
	}
}

func (aw *AppWatcher) processPackage(readmePath string) {
	dirPath := filepath.Dir(readmePath)
	dirName := filepath.Base(dirPath)

	// Create a unique ID that includes the relative path from one of the watched paths
	var uniqueID string
	for _, watchPath := range aw.watchPaths {
		if strings.HasPrefix(dirPath, watchPath) {
			relPath, err := filepath.Rel(watchPath, dirPath)
			if err == nil {
				uniqueID = relPath
				break
			}
		}
	}
	if uniqueID == "" {
		uniqueID = dirName
	}

	metadata, err := aw.extractMetadata(readmePath)
	if err != nil {
		log.Printf("Failed to extract metadata from %s: %v", readmePath, err)
		return
	}

	app := App{
		ID:           uniqueID,
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

	// Save to database instead of memory
	err = saveOrUpdateApp(app)
	if err != nil {
		log.Printf("Failed to save app to database: %v", err)
	}
}

func (aw *AppWatcher) processSMBPackage(readmePath string, shareKey string) {
	// readmePath is already in SMB format: share/path/to/package/README.md
	// Extract relative path from share root
	rest := strings.TrimPrefix(readmePath, shareKey+"/")

	// Create unique ID with SMB prefix - include full path
	uniqueID := fmt.Sprintf("smb:%s/%s", shareKey, rest)
	log.Printf("SMB Package ID: %s from readmePath: %s, shareKey: %s, rest: %s", uniqueID, readmePath, shareKey, rest)

	metadata, err := aw.extractSMBMetadata(readmePath, shareKey)
	if err != nil {
		log.Printf("Failed to extract metadata from SMB %s: %v", readmePath, err)
		return
	}

	app := App{
		ID:           uniqueID,
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

	// Save to database instead of memory
	err = saveOrUpdateApp(app)
	if err != nil {
		log.Printf("Failed to save SMB app to database: %v", err)
	}
}

func (aw *AppWatcher) extractMetadata(filePath string) (PackageMetadata, error) {
	return aw.parseReadmeMetadata(filePath)
}

func (aw *AppWatcher) extractSMBMetadata(readmePath string, shareKey string) (PackageMetadata, error) {
	conn, err := aw.smbManager.GetConnection(shareKey)
	if err != nil {
		return PackageMetadata{}, fmt.Errorf("failed to get SMB connection: %v", err)
	}

	file, err := conn.Share.Open(readmePath)
	if err != nil {
		return PackageMetadata{}, fmt.Errorf("failed to open SMB file %s: %v", readmePath, err)
	}
	defer file.Close()

	// Read file content manually since it's an SMB file
	content := make([]byte, 0, 4096)
	buf := make([]byte, 1024)
	for {
		n, err := file.Read(buf)
		if err != nil && err != io.EOF {
			return PackageMetadata{}, fmt.Errorf("failed to read SMB file %s: %v", readmePath, err)
		}
		if n == 0 {
			break
		}
		content = append(content, buf[:n]...)
	}

	return aw.parseReadmeContent(string(content), filepath.Base(filepath.Dir(readmePath)))
}

func (aw *AppWatcher) parseReadmeContent(contentStr, dirName string) (PackageMetadata, error) {
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
				metadata.Name = dirName
			}
		}
	}

	return metadata, nil
}

func (aw *AppWatcher) parseReadmeMetadata(readmePath string) (PackageMetadata, error) {
	content, err := os.ReadFile(readmePath)
	if err != nil {
		return PackageMetadata{}, err
	}

	dirName := filepath.Base(filepath.Dir(readmePath))
	return aw.parseReadmeContent(string(content), dirName)
}

func initDatabase() error {
	var err error

	// Get database file path from environment, default to data.db
	dbFile := os.Getenv("DATABASE_FILE")
	if dbFile == "" {
		dbFile = "data.db"
	}

	db, err = sql.Open("sqlite", dbFile)
	if err != nil {
		return fmt.Errorf("failed to open database: %v", err)
	}

	// Test the connection
	if err = db.Ping(); err != nil {
		return fmt.Errorf("failed to connect to database: %v", err)
	}

	// Create apps table
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS apps (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		category TEXT NOT NULL,
		description TEXT NOT NULL,
		version TEXT NOT NULL,
		developer TEXT NOT NULL,
		rating REAL NOT NULL,
		downloads INTEGER NOT NULL,
		price TEXT NOT NULL,
		icon TEXT NOT NULL,
		download_link TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);`

	if _, err = db.Exec(createTableSQL); err != nil {
		return fmt.Errorf("failed to create apps table: %v", err)
	}

	log.Println("Database initialized successfully")
	return nil
}

func init() {
	var err error

	// Initialize database first
	if err := initDatabase(); err != nil {
		log.Fatal("Database initialization failed:", err)
	}

	funcMap := template.FuncMap{
		"hasPrefix": strings.HasPrefix,
	}

	templates, err = template.New("").Funcs(funcMap).ParseGlob("templates/*.html")
	if err != nil {
		log.Fatal("Error parsing templates:", err)
	}
}

func saveOrUpdateApp(app App) error {
	// Check if app already exists
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM apps WHERE id = ?", app.ID).Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to check if app exists: %v", err)
	}

	if count > 0 {
		// Update existing app
		updateSQL := `
		UPDATE apps SET 
			name = ?, category = ?, description = ?, version = ?, 
			developer = ?, rating = ?, downloads = ?, price = ?, 
			icon = ?, download_link = ?, updated_at = ?
		WHERE id = ?`

		_, err = db.Exec(updateSQL,
			app.Name, app.Category, app.Description, app.Version,
			app.Developer, app.Rating, app.Downloads, app.Price,
			app.Icon, app.DownloadLink, app.UpdatedAt, app.ID)

		if err != nil {
			return fmt.Errorf("failed to update app: %v", err)
		}
		log.Printf("Updated app in database: %s", app.Name)
	} else {
		// Insert new app
		insertSQL := `
		INSERT INTO apps (
			id, name, category, description, version, developer,
			rating, downloads, price, icon, download_link,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

		_, err = db.Exec(insertSQL,
			app.ID, app.Name, app.Category, app.Description, app.Version,
			app.Developer, app.Rating, app.Downloads, app.Price,
			app.Icon, app.DownloadLink, app.CreatedAt, app.UpdatedAt)

		if err != nil {
			return fmt.Errorf("failed to insert app: %v", err)
		}
		log.Printf("Added new app to database: %s", app.Name)
	}

	return nil
}

func getAppsFromDB(limit int) ([]App, error) {
	query := "SELECT id, name, category, description, version, developer, rating, downloads, price, icon, download_link, created_at, updated_at FROM apps ORDER BY updated_at DESC"

	if limit > 0 {
		query += " LIMIT ?"
	}

	rows, err := db.Query(query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query apps: %v", err)
	}
	defer rows.Close()

	var apps []App
	for rows.Next() {
		var app App
		err := rows.Scan(
			&app.ID, &app.Name, &app.Category, &app.Description,
			&app.Version, &app.Developer, &app.Rating, &app.Downloads,
			&app.Price, &app.Icon, &app.DownloadLink,
			&app.CreatedAt, &app.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan app row: %v", err)
		}
		apps = append(apps, app)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating app rows: %v", err)
	}

	return apps, nil
}

func searchAppsInDB(searchQuery string) ([]App, error) {
	query := `
		SELECT id, name, category, description, version, developer, 
			   rating, downloads, price, icon, download_link, 
			   created_at, updated_at 
		FROM apps 
		WHERE LOWER(name) LIKE ? OR LOWER(category) LIKE ? OR 
			  LOWER(description) LIKE ? OR LOWER(developer) LIKE ?
		ORDER BY updated_at DESC`

	searchPattern := "%" + strings.ToLower(searchQuery) + "%"

	rows, err := db.Query(query, searchPattern, searchPattern, searchPattern, searchPattern)
	if err != nil {
		return nil, fmt.Errorf("failed to search apps: %v", err)
	}
	defer rows.Close()

	var apps []App
	for rows.Next() {
		var app App
		err := rows.Scan(
			&app.ID, &app.Name, &app.Category, &app.Description,
			&app.Version, &app.Developer, &app.Rating, &app.Downloads,
			&app.Price, &app.Icon, &app.DownloadLink,
			&app.CreatedAt, &app.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan app row: %v", err)
		}
		apps = append(apps, app)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating app rows: %v", err)
	}

	return apps, nil
}

func getAppByIDFromDB(appID string) (*App, error) {
	query := `
		SELECT id, name, category, description, version, developer, 
			   rating, downloads, price, icon, download_link, 
			   created_at, updated_at 
		FROM apps WHERE id = ?`

	var app App
	err := db.QueryRow(query, appID).Scan(
		&app.ID, &app.Name, &app.Category, &app.Description,
		&app.Version, &app.Developer, &app.Rating, &app.Downloads,
		&app.Price, &app.Icon, &app.DownloadLink,
		&app.CreatedAt, &app.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // App not found
		}
		return nil, fmt.Errorf("failed to get app by ID: %v", err)
	}

	return &app, nil
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	latestApps, err := getAppsFromDB(10)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get apps: %v", err), http.StatusInternalServerError)
		return
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

	var apps []App
	var err error

	if searchQuery != "" {
		apps, err = searchAppsInDB(searchQuery)
	} else {
		apps, err = getAppsFromDB(0) // 0 means no limit
	}

	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get apps: %v", err), http.StatusInternalServerError)
		return
	}

	data := struct {
		Config      Config
		Apps        []App
		SearchQuery string
		TotalCount  int
	}{
		Config:      appConfig,
		Apps:        apps,
		SearchQuery: searchQuery,
		TotalCount:  len(apps),
	}

	renderTemplate(w, "apps.html", data)
}

func appDetailHandler(w http.ResponseWriter, r *http.Request) {
	appID := r.URL.Path[len("/app/"):]

	app, err := getAppByIDFromDB(appID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get app: %v", err), http.StatusInternalServerError)
		return
	}

	if app == nil {
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
		App:                *app,
		DownloadsFormatted: downloadsFormatted,
	}

	renderTemplate(w, "detail.html", data)
}

func downloadHandler(w http.ResponseWriter, r *http.Request) {
	appID := r.URL.Path[len("/download/"):]

	log.Printf("Download request for AppID: '%s'", appID)
	app, err := getAppByIDFromDB(appID)
	if err != nil {
		log.Printf("Database error getting app '%s': %v", appID, err)
		http.Error(w, fmt.Sprintf("Failed to get app: %v", err), http.StatusInternalServerError)
		return
	}

	if app == nil {
		log.Printf("App not found in database: '%s'", appID)
		http.NotFound(w, r)
		return
	}

	log.Printf("Found app: %+v", *app)

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

	// Check if this is an SMB app
	if strings.HasPrefix(app.ID, "smb:") {
		serveSMBFile(w, r, *app)
		return
	}

	// Otherwise, treat as local file path and proxy the download
	// Try to find the app in any of the watched directories
	var filePath string
	watchPaths := strings.Split(os.Getenv("MANAGED_FOLDERS"), ",")
	if watchPaths[0] == "" {
		watchPaths = []string{"packages"}
	}

	for _, watchPath := range watchPaths {
		watchPath = strings.TrimSpace(watchPath)
		// Skip SMB paths for local file serving
		if strings.HasPrefix(watchPath, "\\\\") {
			continue
		}
		testPath := filepath.Join(watchPath, appID, app.DownloadLink)
		if _, err := os.Stat(testPath); err == nil {
			filePath = testPath
			break
		}
	}

	if filePath == "" {
		// Fallback to first local watched path
		for _, watchPath := range watchPaths {
			watchPath = strings.TrimSpace(watchPath)
			if !strings.HasPrefix(watchPath, "\\\\") {
				filePath = filepath.Join(watchPath, appID, app.DownloadLink)
				break
			}
		}
	}

	http.ServeFile(w, r, filePath)
}

func serveSMBFile(w http.ResponseWriter, r *http.Request, app App) {
	// The app.ID comes from database and should already be correctly formatted
	// URL decode the app ID first (to handle Chinese characters)
	decodedID, err := url.QueryUnescape(app.ID)
	if err != nil {
		log.Printf("Failed to decode app ID: %v", err)
		decodedID = app.ID
	} else {
		log.Printf("SMB Download - Decoded AppID: %s, Original: %s", decodedID, app.ID)
	}

	// Handle escaped forward slashes in HTML (\/ -> /)
	normalizedID := strings.ReplaceAll(decodedID, "\\/", "/")

	// Parse SMB app ID to get share info
	// Format: smb:host/share/path/to/package (where share can contain Chinese characters)
	rest := strings.TrimPrefix(normalizedID, "smb:")

	// Split by first slash to separate host from rest of path
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 2 {
		http.Error(w, "Invalid SMB app ID format", http.StatusInternalServerError)
		return
	}

	host := parts[0]
	fullPath := parts[1] // This contains share/path/to/package

	// Extract just the directory path (remove filename)
	dirPath := filepath.Dir(fullPath)

	// Split directory path by first slash to separate share from relative path
	pathParts := strings.SplitN(dirPath, "/", 2)
	share := pathParts[0]
	relativePath := ""
	if len(pathParts) > 1 {
		relativePath = pathParts[1]
	}

	shareKey := fmt.Sprintf("%s/%s", host, share)

	log.Printf("SMB Download - AppID: %s, ShareKey: %s, RelativePath: %s, DownloadLink: %s",
		app.ID, shareKey, relativePath, app.DownloadLink)

	// Get SMB connection
	conn, err := globalSMBManager.GetConnection(shareKey)
	if err != nil {
		http.Error(w, fmt.Sprintf("SMB connection error: %v", err), http.StatusInternalServerError)
		return
	}

	// Clean up download link - convert backslashes to forward slashes and extract just the filename
	cleanDownloadLink := strings.ReplaceAll(app.DownloadLink, "\\", "/")
	if strings.Contains(cleanDownloadLink, "/") {
		parts := strings.Split(cleanDownloadLink, "/")
		cleanDownloadLink = parts[len(parts)-1] // Get just the filename
	}

	// Construct full file path - use forward slashes for SMB
	filePath := strings.Join([]string{relativePath, cleanDownloadLink}, "/")

	log.Printf("SMB Download - Final file path: %s", filePath)

	// Open SMB file
	file, err := conn.Share.Open(filePath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to open SMB file: %v", err), http.StatusNotFound)
		return
	}
	defer file.Close()

	// Get file info for headers
	fileInfo, err := file.Stat()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get SMB file info: %v", err), http.StatusInternalServerError)
		return
	}

	// Set headers
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filepath.Base(app.DownloadLink)))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", fileInfo.Size()))

	// Stream file content
	_, err = io.Copy(w, file)
	if err != nil {
		log.Printf("Error streaming SMB file: %v", err)
	}
}

func renderTemplate(w http.ResponseWriter, templateName string, data interface{}) {
	err := templates.ExecuteTemplate(w, templateName, data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func main() {
	// Load .env file
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using default values")
	}

	// Get managed folders from environment
	managedFoldersEnv := os.Getenv("MANAGED_FOLDERS")
	var watchPaths []string

	if managedFoldersEnv == "" {
		watchPaths = []string{"./packages"}
	} else {
		folders := strings.Split(managedFoldersEnv, ",")
		for _, folder := range folders {
			watchPaths = append(watchPaths, strings.TrimSpace(folder))
		}
	}

	// Create directories if they don't exist
	for _, watchPath := range watchPaths {
		if err := os.MkdirAll(watchPath, 0755); err != nil {
			log.Fatal("Failed to create watch directory:", watchPath, err)
		}
	}

	watcher, err := NewAppWatcher(watchPaths)
	if err != nil {
		log.Fatal("Failed to create watcher:", err)
	}

	globalSMBManager = watcher.smbManager

	if err := watcher.Start(); err != nil {
		log.Fatal("Failed to start watcher:", err)
	}
	defer watcher.Stop()
	defer globalSMBManager.CloseAll()
	defer db.Close()

	http.HandleFunc("/", homeHandler)
	http.HandleFunc("/apps", appListHandler)
	http.HandleFunc("/app/", appDetailHandler)
	http.HandleFunc("/download/", downloadHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	port = ":" + port

	fmt.Printf("Server starting on port %s...\n", port)
	fmt.Printf("Home: http://localhost%s\n", port)
	fmt.Printf("Apps: http://localhost%s/apps\n", port)
	fmt.Printf("Watching directories: %v\n", watchPaths)

	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatal("Server error:", err)
	}
}

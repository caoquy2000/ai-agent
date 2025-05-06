// main.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-resty/resty/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type WatchItem struct {
	Type     string   `json:"type"`
	Keywords []string `json:"keywords"`
	Users    []string `json:"users,omitempty"`
}

var (
	newsAPIKey    = os.Getenv("NEWSAPI_KEY")
	twitterBearer = os.Getenv("TWITTER_BEARER")
	mongoURI      = os.Getenv("MONGO_URI")
	watchlist     []WatchItem
	watchlistMux  sync.Mutex
	client        = resty.New()
	mongoClient   *mongo.Client
	collection    *mongo.Collection
)

func loadMongo() {
	ctx := context.TODO()
	cli, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		log.Fatal(err)
	}
	mongoClient = cli
	collection = cli.Database("news_ai_agent").Collection("raw_articles")
}

func saveToMongo(item map[string]interface{}) {
	ctx := context.TODO()
	filter := bson.M{"url": item["url"]}
	update := bson.M{"$set": item}
	opts := options.Update().SetUpsert(true)
	_, _ = collection.UpdateOne(ctx, filter, update, opts)
}

func fetchNews(keyword string) {
	resp, err := client.R().
		SetQueryParams(map[string]string{
			"q":        keyword,
			"language": "en",
			"sortBy":   "publishedAt",
			"pageSize": "10",
			"apiKey":   newsAPIKey,
		}).
		SetHeader("Accept", "application/json").
		Get("https://newsapi.org/v2/everything")

	if err != nil {
		log.Println("NewsAPI error:", err)
		return
	}

	var result map[string][]map[string]interface{}
	_ = json.Unmarshal(resp.Body(), &result)
	for _, article := range result["articles"] {
		article["source_type"] = "newsapi"
		article["fetched_at"] = time.Now()
		saveToMongo(article)
	}
}

func fetchTwitter(keyword string) {
	resp, err := client.R().
		SetHeader("Authorization", "Bearer "+twitterBearer).
		SetQueryParams(map[string]string{
			"query":       keyword,
			"max_results": "10",
			"tweet.fields": "author_id,created_at",
		}).
		Get("https://api.twitter.com/2/tweets/search/recent")

	if err != nil {
		log.Println("Twitter error:", err)
		return
	}

	var data map[string][]map[string]interface{}
	_ = json.Unmarshal(resp.Body(), &data)
	for _, tweet := range data["data"] {
		tweet["source_type"] = "twitter"
		tweet["fetched_at"] = time.Now()
		tweet["url"] = fmt.Sprintf("https://twitter.com/i/web/status/%v", tweet["id"])
		saveToMongo(tweet)
	}
}

func scheduler() {
	for {
		watchlistMux.Lock()
		localList := make([]WatchItem, len(watchlist))
		copy(localList, watchlist)
		watchlistMux.Unlock()

		for _, item := range localList {
			if item.Type == "newsapi" {
				for _, kw := range item.Keywords {
					fetchNews(kw)
				}
			} else if item.Type == "twitter" {
				for _, kw := range item.Keywords {
					fetchTwitter(kw)
				}
			}
		}
		time.Sleep(2 * time.Minute)
	}
}

func apiServer() {
	r := chi.NewRouter()

	r.Get("/watchlist", func(w http.ResponseWriter, r *http.Request) {
		watchlistMux.Lock()
		defer watchlistMux.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(watchlist)
	})

	r.Post("/watchlist", func(w http.ResponseWriter, r *http.Request) {
		var newList []WatchItem
		if err := json.NewDecoder(r.Body).Decode(&newList); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		watchlistMux.Lock()
		watchlist = newList
		watchlistMux.Unlock()
		w.WriteHeader(http.StatusOK)
	})

	log.Println("API server running on :8080")
	http.ListenAndServe(":8080", r)
}

func main() {
	loadMongo()
	watchlist = []WatchItem{} // start empty
	go scheduler()
	apiServer()
}

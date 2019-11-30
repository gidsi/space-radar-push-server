package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/oliveroneill/exponent-server-sdk-golang/sdk"
	"github.com/robfig/cron"
	"github.com/rs/cors"
	"goji.io"
	"goji.io/pat"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type appRegister struct {
	Token	string `json:"token"`
	FavoriteSpace	[]string `json:"favoriteSpace"`
}

type collectorEntry struct {
	Url      string `json:"url"`
	Valid    bool   `json:"valid"`
	LastSeen int64  `json:"lastSeen,omitempty"`
	ErrMsg   string `json:"errMsg,omitempty"`
	Data	 dataEntry `json:"data,omitempty"`
}

type dataEntry struct {
	State state `json:"state"`
	Space string `json:"space"`
}

type state struct {
	Open bool `json:"open"`
	LastChange int64 `json:"lastchange"`
}

var mongoDb = "spaceradar"
var directory map[string]collectorEntry

func getMongoSession() *mgo.Session {
	mongoSession, err := mgo.Dial("database")

	if err != nil {
		panic(err)
	}

	mongoSession.SetMode(mgo.Monotonic, true)
	return mongoSession
}

func main() {
	sigs := make(chan os.Signal, 1)
	done := make(chan bool, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)


	log.Println("starting service...")
	loadPersistentDirectory()

	c := cron.New()
	err := c.AddFunc("@every 1m", func() {
		checkDirectory()
	})
	if err != nil {
		log.Printf("Can't start rebuilding directory cron %v", err)
	} else {
		c.Start()
	}
	co := cors.New(cors.Options{
		AllowedOrigins: []string{"*"},
	})

	mux := goji.NewMux()
	mux.Use(co.Handler)

	mux.HandleFunc(pat.Post("/users/push-token"), registerUser)

	srv := &http.Server{Addr: ":8080", Handler: mux}
	go func() {
		httpError := srv.ListenAndServe()
		if httpError != nil {
			log.Println("While serving HTTP: ", httpError)
		}
	}()


	go func() {
		<-sigs
		ctx, _ := context.WithTimeout(context.Background(), 10 * time.Second)
		err = srv.Shutdown(ctx)
		if err != nil {
			log.Fatal(err)
		}
		done <- true
	}()

	log.Println("service started")
	<-done
	log.Println("service closed")
}

func checkDirectory() {
	log.Println("Checking directory")
	newDirectory := getDirectory()

	for url, value := range newDirectory {
		state := value.Data.State
		for oUrl, oValue := range directory {
			if oUrl == url {
				oState := oValue.Data.State
				if state.Open != oState.Open {
					pushSpaceChange(url, state)
				}
			}
		}
	}

	directory = newDirectory
	persistDirectory()
}

func pushSpaceChange(url string, state state) {
	space := directory[url]
	open := "closed"
	if state.Open {
		open = "open"
	}
	message := fmt.Sprintf("status changed to %s", open)

	mongoSession := getMongoSession()
	defer mongoSession.Close()

	var result []appRegister
	err := mongoSession.DB(mongoDb).C("registrations").Find(bson.M{"favoritespace": url}).All(&result)
	if err != nil {
		log.Println(err)
		return
	}

	log.Printf("Sending push message: '%s', for space: %s to %d recipients", message, space.Data.Space, len(result))
	log.Printf("Spacedata: %v", space)
	for _, registration := range result {
		sendPushMessage(registration.Token, space.Data.Space, message)
	}
}

func loadPersistentDirectory() bool {
	log.Println("reading...")
	fileContent, err := ioutil.ReadFile("/tmp/spaceApiDirectory.json")
	if err != nil {
		log.Println(err)
		log.Println("can't read directory file, skipping...")
		return false
	}
	err = json.Unmarshal(fileContent, &directory)
	if err != nil {
		log.Println(err)
		panic("can't unmarshal api directory")
	}

	return true
}

func persistDirectory() {
	spaceApiDirectoryJson, err := json.Marshal(directory)
	if err != nil {
		log.Println(err)
		panic("can't marshall api directory")
	}
	err = ioutil.WriteFile("/tmp/spaceApiDirectory.json", []byte(spaceApiDirectoryJson), 0644)
	if err != nil {
		log.Println(err)
		panic("can't write api directory to file")
	}
}

func getDirectory() map[string]collectorEntry {
	resp, err := http.Get("https://api.spaceapi.io/collector")

	if err != nil {
		log.Println(err)
	}
	defer func() {
		err := resp.Body.Close()
		if err != nil {
			panic(err)
		}
	}()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println(err)
	}

	staticDirectory := make(map[string]collectorEntry)
	var responseDirectory []collectorEntry
	err = json.Unmarshal(body, &responseDirectory)

	for _, entry := range responseDirectory {
		staticDirectory[entry.Url] = entry
	}

	return staticDirectory
}

func registerUser(w http.ResponseWriter, r *http.Request) {
	var appRegister appRegister

	err := json.NewDecoder(r.Body).Decode(&appRegister)
	if err != nil {
		log.Println(err)
		w.WriteHeader(400)
		return
	}

	err = save(appRegister)
	if err != nil {
		log.Println(err)
		w.WriteHeader(500)
		return
	}
}

func sendPushMessage(token string, title string, message string) {
	pushToken, err := expo.NewExponentPushToken(token)
	if err != nil {
		panic(err)
	}

	// Create a new Expo SDK client
	client := expo.NewPushClient(nil)

	// Publish message
	response, err := client.Publish(
		&expo.PushMessage{
			To:       pushToken,
			Body:     message,
			Data:     map[string]string{"withSome": "data"},
			Sound:    "default",
			Title:    title,
			Priority: expo.DefaultPriority,
		},
	)
	// Check errors
	if err != nil {
		panic(err)
		return
	}
	// Validate responses
	if response.ValidateResponse() != nil {
		fmt.Println(response)
		fmt.Println(response.PushMessage.To, "failed")
	}
}

func save(registration appRegister) error {
	mongoSession := getMongoSession()
	defer mongoSession.Close()

	upsertdata := bson.M{ "$set": registration}

	_, err := mongoSession.DB(mongoDb).C("registrations").UpsertId( registration.Token, upsertdata )
	if err != nil {
		return err
	}

	result := appRegister{}
	err = mongoSession.DB(mongoDb).C("registrations").FindId(registration.Token).One(&result)
	if err != nil {
		return err
	}

	return nil
}

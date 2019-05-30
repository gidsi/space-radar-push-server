package main

import (
	"encoding/json"
	"fmt"
	"github.com/oliveroneill/exponent-server-sdk-golang/sdk"
	"github.com/pkg/errors"
	"github.com/rs/cors"
	"goji.io"
	"goji.io/pat"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	"io/ioutil"
	"log"
	"net/http"
	"reflect"
	"strings"
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
	Data	 interface{} `json:"data,omitempty"`
}

type state struct {
	Open bool
	LastChange int64
}

var mongoSession *mgo.Session
var mongoDb = "spaceradar"
var directory map[string]collectorEntry

func init() {
	mongoSession, err := mgo.Dial("database")

	if err != nil {
		log.Print("Error: ", err)
		return
	}

	mongoSession.SetMode(mgo.Monotonic, true)
}

func main() {
	loadPersistentDirectory()

	/* c := cron.New()
	err := c.AddFunc("@every 1m", func() {
		checkDirectory()
	})
	if err != nil {
		log.Printf("Can't start rebuilding directory cron %v", err)
	} else {
		c.Start()
	} */
	checkDirectory()
	checkDirectory()

	co := cors.New(cors.Options{
		AllowedOrigins: []string{"*"},
	})

	mux := goji.NewMux()
	mux.Use(co.Handler)

	mux.HandleFunc(pat.Post("/users/push-token"), registerUser)
	mux.HandleFunc(pat.Get("/push"), push)

	defer mongoSession.Close()

	log.Println("starting api...")
	log.Fatal(http.ListenAndServe(":8080", mux))
}

func checkDirectory() {
	newDirectory := getDirectory()

	for url, value := range newDirectory {
		fmt.Println(url)
		state, err := getState(value)
		if err != nil {
			for oUrl, oValue := range directory {
				if oUrl == url {
					oState, oErr := getState(oValue)
					if oErr != nil {
						fmt.Println("didn't work on old state")
						fmt.Println(state)
						fmt.Println(oState)
						fmt.Println(oErr)
					} else {
						fmt.Println("NOT AN ERROR!")
					}
				}
			}
		}
	}

	directory = newDirectory
	persistDirectory()
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
	log.Println("writing...")
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

func getState(entry collectorEntry) (state, error) {
	ret := state{}
	if entry.Data == nil {
		return ret, errors.New("no data")
	}

	foo := reflect.ValueOf(entry.Data)
	value, err := getKeyFromValueMap("state", foo)
	if err != nil {
		return state{}, err
	} else {
		fmt.Println("state is")
		fmt.Println(value)
	}

	openVal, err := getKeyFromValueMap("open", value)
	if err != nil {
		fmt.Println("openVal is err and")
		fmt.Println(value)
		return state{}, err
	} else {
		fmt.Println("openVal is")
		fmt.Println(openVal.Bool())
		ret.Open = openVal.Bool()
	}

	lastChangeVal, err := getKeyFromValueMap("lastchange", value)
	if err != nil {
		ret.LastChange = 0
	} else {
		ret.LastChange = lastChangeVal.Int()
	}

	return ret, nil
}

func getKeyFromValueMap(key string, entry reflect.Value) (reflect.Value, error) {
	if entry.IsValid() {
		if entry.Kind() == reflect.Map {
			for _, b := range entry.MapKeys() {
				if b.String() == key {
					return entry.MapIndex(b), nil
				}
			}
		} else if entry.Kind() == reflect.Interface {
			fmt.Println("interface")
			fmt.Println(entry.Interface())
			foo := entry.Interface()

			// entry.FieldByName(key)
		}
	}

	return reflect.Value{}, errors.New("cant kind key: " + key)
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

func push(w http.ResponseWriter, _ *http.Request) {
	var result []appRegister
	err := mongoSession.DB(mongoDb).C("registrations").Find(nil).All(&result)
	if err != nil {
		log.Println(err)
		return
	}

	for _, registration := range result {
		sendPushMessage(registration.Token, strings.Join(registration.FavoriteSpace, ",\n"))
	}
	w.WriteHeader(200)
}

func sendPushMessage(token string, message string) {
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
			Title:    "status changed",
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
package main

import (
	"log"
	"os"
	"runtime"

	"gopkg.in/yaml.v3"
)

const (
	apiVersion      = 1
	confPath        = "conf.yaml"
	clientsMaxNum   = uint32(1000000)
	driversMaxNum   = uint32(500000)
	adminMasterCode = uint64(0x105F218E5BFEC13E)
	masterCode      = uint64(0x2D082E4CD3339301)
)

//-------------------------------------------------------------------------------------------------------

var (
	adminMasterKey = []byte{0xA8, 0xFA, 0x08, 0x03, 0xDE, 0x6C, 0xF6, 0x25, 0xB1, 0xD5, 0xB9, 0x91, 0x1D, 0xA5, 0x56, 0xD6}
	masterKey      = []byte{0xF1, 0xE5, 0xB8, 0x27, 0xDF, 0x61, 0x39, 0x27, 0x11, 0x4B, 0x31, 0x7A, 0x2A, 0x91, 0xCE, 0x79}
)

//-------------------------------------------------------------------------------------------------------

type config struct {
	AdminPort  string `yaml:"adminPort"`
	ClientPort string `yaml:"clientPort"`
	DriverPort string `yaml:"driverPort"`
}

//-------------------------------------------------------------------------------------------------------

func loadConfig(path string) (*config, error) {
	conf := &config{}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	d := yaml.NewDecoder(file)
	if err := d.Decode(&conf); err != nil {
		return nil, err
	}
	return conf, nil
}

//-------------------------------------------------------------------------------------------------------

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	conf, err := loadConfig(confPath)
	if err != nil {
		log.Fatalf("Unable to load config: %v", err)
	}
	log.Println("---------- Server started ----------")

	go func() {
		log.Fatal(serveClient(conf.ClientPort))
	}()

	go func() {
		log.Fatal(serveDriver(conf.DriverPort))
	}()

	log.Fatal(serveAdmin(conf.AdminPort))
}

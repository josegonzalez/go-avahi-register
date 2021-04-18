package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/akamensky/argparse"
	"github.com/josegonzalez/mdns"
	"github.com/radovskyb/watcher"
)

type Service struct {
	Name     string `json:"name"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Scheme   string `json:"scheme"`
}

type Registry struct {
	Services []Service `json:"services"`
}

var (
	publishedServices = map[string]Service{}
	publishedTypes    = map[string]bool{}
	registry          *Registry
	registryLock      = new(sync.RWMutex)
	Version           string
	zone              *mdns.Zone
	zoneLock          = new(sync.RWMutex)
)

func NewService(name string, port int, protocol string, scheme string) *Service {
	if port == 0 {
		port = 80
	}

	if protocol == "" {
		protocol = "tcp"
	}

	return &Service{
		Name:     name,
		Port:     port,
		Protocol: protocol,
		Scheme:   scheme,
	}
}

func (s *Service) Equals(o Service) bool {
	return s.String() == o.String()
}

func (s *Service) String() string {
	return fmt.Sprintf("%v+%v://%v.local:%v", s.Scheme, s.Protocol, s.Name, s.Port)
}

func (s *Service) Type() string {
	t := fmt.Sprintf("_%v._%v.local.", s.Scheme, s.Protocol)
	if s.Scheme == "" {
		t = fmt.Sprintf("_%v.local.", s.Protocol)
	}

	return t
}

func (s *Service) Validate() error {
	if s.Name == "" {
		return errors.New(`Service "name" field is required`)
	}

	tcpServices := map[string]bool{
		"http":  true,
		"https": true,
	}

	if tcpServices[s.Scheme] && s.Protocol != "tcp" {
		return errors.New(fmt.Sprintf(`Service "%s" with scheme "%s" must use "tcp" protocol`,
			s.Name, s.Scheme))
	}

	return nil
}

func getIPAddress() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}

	for _, address := range addrs {
		// check the address type and if it is not a loopback the display it
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return ""
}

func getReverseIPAddress(ipAddress string) string {
	pieces := strings.Split(ipAddress, ".")
	for i := len(pieces)/2 - 1; i >= 0; i-- {
		opp := len(pieces) - 1 - i
		pieces[i], pieces[opp] = pieces[opp], pieces[i]
	}

	return strings.Join(pieces, ".")
}

func loadRegistry(configFile string) (err error) {
	data, err := ioutil.ReadFile(configFile)
	if err != nil {
		return err
	}

	temp := new(Registry)
	err = json.Unmarshal(data, temp)
	if err != nil {
		return err
	}

	var services []Service
	for _, service := range temp.Services {
		s, err := hydrateService(service)
		if err != nil {
			return err
		}

		services = append(services, s)
	}

	temp.Services = services

	registryLock.Lock()
	registry = temp
	registryLock.Unlock()

	return nil
}

func hydrateService(s Service) (Service, error) {
	service := *NewService(s.Name, s.Port, s.Protocol, s.Scheme)
	if err := service.Validate(); err != nil {
		return service, err
	}

	return service, nil
}

func getRegistry() *Registry {
	registryLock.RLock()
	defer registryLock.RUnlock()
	return registry
}

func publishServices(ipAddress string, reverseIPAddress string) error {
	r := getRegistry()

	zoneLock.Lock()
	seenServices := map[string]bool{}
	seenTypes := map[string]bool{}

	for _, service := range r.Services {
		name := service.Name
		serviceType := service.Type()

		seenServices[service.String()] = true
		seenTypes[serviceType] = true

		if _, ok := publishedServices[service.String()]; !ok {
			log.Println("publishing", service.String())
			publishService(service, ipAddress, reverseIPAddress)
			publishedServices[service.String()] = service
		}

		if !publishedTypes[serviceType] {
			log.Println("registering type", serviceType)
			zone.Publish(fmt.Sprintf("_services._dns-sd._udp.local. 60 IN PTR %v", serviceType))
			publishedTypes[serviceType] = true
		}

		zone.Publish(fmt.Sprintf("%v.%v 60 IN SRV 0 0 %v %[1]v.local.", name, serviceType, service.Port))
	}

	for _, service := range publishedServices {
		if !seenServices[service.String()] {
			log.Println("unpublishing", service.String())
			unpublishService(service, ipAddress, reverseIPAddress)
			delete(publishedServices, service.String())
		}
	}

	for serviceType := range publishedTypes {
		if !seenTypes[serviceType] {
			log.Println("deregistering type", serviceType)
			zone.Unpublish(fmt.Sprintf("_services._dns-sd._udp.local. 60 IN PTR %v", serviceType))
			delete(publishedTypes, serviceType)
		}
	}

	zoneLock.Unlock()
	return nil
}

func publishService(service Service, ipAddress string, reverseIPAddress string) {
	zone.Publish(fmt.Sprintf("%v.local. 60 IN A %v", service.Name, ipAddress))
	zone.Publish(fmt.Sprintf("%v.in-addr.arpa. 60 IN PTR %s.local.", reverseIPAddress, service.Name))
	zone.Publish(fmt.Sprintf("%v 60 IN PTR %v.%[1]v", service.Type(), service.Name))
	zone.Publish(fmt.Sprintf(`%v.%v 60 IN TXT ""`, service.Name, service.Type()))
}

func unpublishService(service Service, ipAddress string, reverseIPAddress string) {
	zone.Unpublish(fmt.Sprintf("%v.local. 60 IN A %v", service.Name, ipAddress))
	zone.Unpublish(fmt.Sprintf("%v.in-addr.arpa. 60 IN PTR %s.local.", reverseIPAddress, service.Name))
	zone.Unpublish(fmt.Sprintf("%v 60 IN PTR %v.%[1]v", service.Type(), service.Name))
	zone.Unpublish(fmt.Sprintf(`%v.%v 60 IN TXT ""`, service.Name, service.Type()))
	zone.Unpublish(fmt.Sprintf("%v.%v 60 IN SRV 0 0 %v %[1]v.local.", service.Name, service.Type(), service.Port))
}

func addCommand(configFile string, name string, port int, scheme string, protocol string) int {
	if err := loadRegistry(configFile); err != nil {
		log.Println("err:", err)
		return 1
	}

	service := *NewService(name, port, protocol, scheme)
	if err := service.Validate(); err != nil {
		log.Println("err:", err)
		return 1
	}

	for _, s := range registry.Services {
		if s.Equals(service) {
			log.Println(fmt.Sprintf("Service %s already exists", service.String()))
			return 0
		}
	}

	registry.Services = append(registry.Services, service)
	file, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		log.Println("err:", err)
		return 1
	}

	if err = ioutil.WriteFile(configFile, file, 0644); err != nil {
		log.Println("err:", err)
		return 1
	}

	return 0
}

func catCommand(configFile string) int {
	if err := loadRegistry(configFile); err != nil {
		log.Println("err:", err)
		return 1
	}

	b, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		log.Println("err:", err)
		return 1
	}

	fmt.Println(string(b))
	return 0
}

func initCommand(configFile string) int {
	if err := loadRegistry(configFile); err != nil {
		registry = new(Registry)
	}

	file, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		log.Println("err:", err)
		return 1
	}

	if err = ioutil.WriteFile(configFile, file, 0644); err != nil {
		log.Println("err:", err)
		return 1
	}

	return 0
}

func removeCommand(configFile string, name string, port int, scheme string, protocol string) int {
	if err := loadRegistry(configFile); err != nil {
		log.Println("err:", err)
		return 1
	}

	service := *NewService(name, port, protocol, scheme)
	if err := service.Validate(); err != nil {
		log.Println("err:", err)
		return 1
	}

	var services []Service
	for _, s := range registry.Services {
		if !s.Equals(service) {
			services = append(services, s)
		}
	}

	registry.Services = services
	file, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		log.Println("err:", err)
		return 1
	}

	if err = ioutil.WriteFile(configFile, file, 0644); err != nil {
		log.Println("err:", err)
		return 1
	}

	return 0
}

func runCommand(configFile string, ipAddress string) int {
	z, err := mdns.New()
	if err != nil {
		log.Println("err:", err)
		return 1
	}

	zone = z

	reverseIPAddress := getReverseIPAddress(ipAddress)

	reloadServices := func() error {
		if err := loadRegistry(configFile); err != nil {
			return err
		}

		if err := publishServices(ipAddress, reverseIPAddress); err != nil {
			return err
		}
		return nil
	}

	log.Println("registering services to", ipAddress)
	if err = reloadServices(); err != nil {
		log.Println("err:", err)
		return 1
	}

	w := watcher.New()
	w.SetMaxEvents(1)
	if err := w.Add(configFile); err != nil {
		log.Println("err:", err)
		return 1
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGQUIT,
		syscall.SIGTERM,
		syscall.SIGUSR2)

	e := make(chan int)
	go func() {
		for {
			s := <-c
			switch s {
			// kill -SIGHUP XXXX
			case syscall.SIGHUP:
				log.Println("received SIGHUP")
				if err := reloadServices(); err != nil {
					log.Println("err:", err)
					w.Close()
					e <- 1
				}

			// kill -SIGUSR2 XXXX
			case syscall.SIGUSR2:
				log.Println("received SIGUSR2")
				if err := reloadServices(); err != nil {
					log.Println("err:", err)
					w.Close()
					e <- 1
				}

			case syscall.SIGINT:
				log.Println("received SIGINT")
				w.Close()
				e <- 0

			case syscall.SIGQUIT:
				log.Println("received SIGQUIT")
				w.Close()
				e <- 0

			case syscall.SIGTERM:
				log.Println("received SIGTERM")
				w.Close()
				e <- 0

			default:
				log.Println("received unhandled signal")
				w.Close()
				e <- 1
			}
		}
	}()

	go func() {
		for {
			select {
			case event := <-w.Event:
				if event.Op == watcher.Remove {
					log.Println("config file removed")
					e <- 1
				} else if event.Op == watcher.Move || event.Op == watcher.Rename {
					log.Println("config file moved")
					e <- 1
				} else if event.Op == watcher.Write {
					log.Println("config file updated")
					if err := reloadServices(); err != nil {
						log.Println("err:", err)
						e <- 1
					}
				}

			case err := <-w.Error:
				log.Println("err:", err)
				w.Close()
				e <- 1

			case <-w.Closed:
				log.Println("watcher closed")
				e <- 0
				return
			}
		}
	}()

	// Start the watching process - it'll check for changes every 100ms.
	if err := w.Start(time.Millisecond * 100); err != nil {
		log.Println("err:", err)
		e <- 1
	}

	code := <-e
	return code
}

func showConfigCommand(configFile string) int {
	if err := loadRegistry(configFile); err != nil {
		log.Println("err:", err)
		return 1
	}

	for _, service := range registry.Services {
		fmt.Println(service.String())
	}

	return 0
}

func main() {
	parser := argparse.NewParser("avahi-register", "A tool for registering services against avahi/bonjour")
	configFileFlag := parser.String("c", "config", &argparse.Options{Default: "/etc/avahi-register/config.json", Help: "path to the config.json config file"})
	versionFlag := parser.Flag("v", "version", &argparse.Options{Help: "show version"})

	addCmd := parser.NewCommand("add", "add an entry to the config file")
	nameAddFlag := addCmd.String("n", "name", &argparse.Options{Help: "name of the service", Required: true})
	portAddFlag := addCmd.Int("p", "port", &argparse.Options{Default: 80, Help: "port on which the service is listening"})
	schemeAddFlag := addCmd.String("s", "scheme", &argparse.Options{Default: "http", Help: "scheme of the service"})
	protocolAddFlag := addCmd.String("r", "protocol", &argparse.Options{Default: "tcp", Help: "protocol of the service"})

	catCmd := parser.NewCommand("cat", "cat the config file")

	initCmd := parser.NewCommand("init", "init a the config file if necessary")

	removeCmd := parser.NewCommand("remove", "remove an entry from the config file")
	nameRemoveFlag := removeCmd.String("n", "name", &argparse.Options{Help: "name of the service", Required: true})
	portRemoveFlag := removeCmd.Int("p", "port", &argparse.Options{Default: 80, Help: "port on which the service is listening"})
	schemeRemoveFlag := removeCmd.String("s", "scheme", &argparse.Options{Default: "http", Help: "scheme of the service"})
	protocolRemoveFlag := removeCmd.String("r", "protocol", &argparse.Options{Default: "tcp", Help: "protocol of the service"})

	defaultIpAddress := getIPAddress()
	ipAddressRequired := defaultIpAddress == ""
	runCmd := parser.NewCommand("run", "run the avahi-register process")
	ipAddressRunFlag := runCmd.String("i", "ip-address", &argparse.Options{Default: defaultIpAddress, Help: "a hardcoded IP address", Required: ipAddressRequired})

	showConfigCmd := parser.NewCommand("show-config", "show the config file in a readable format")

	if err := parser.Parse(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", parser.Usage(err))
		os.Exit(1)
		return
	}

	if *versionFlag {
		fmt.Printf("procfile-util %v\n", Version)
		os.Exit(0)
		return
	}

	exitCode := 1
	if addCmd.Happened() {
		exitCode = addCommand(*configFileFlag, *nameAddFlag, *portAddFlag, *schemeAddFlag, *protocolAddFlag)
	} else if catCmd.Happened() {
		exitCode = catCommand(*configFileFlag)
	} else if initCmd.Happened() {
		exitCode = initCommand(*configFileFlag)
	} else if removeCmd.Happened() {
		exitCode = removeCommand(*configFileFlag, *nameRemoveFlag, *portRemoveFlag, *schemeRemoveFlag, *protocolRemoveFlag)
	} else if runCmd.Happened() {
		exitCode = runCommand(*configFileFlag, *ipAddressRunFlag)
	} else if showConfigCmd.Happened() {
		exitCode = showConfigCommand(*configFileFlag)
	} else {
		fmt.Print(parser.Usage(nil))
	}

	os.Exit(exitCode)
}

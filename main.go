package main

import (
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"
)

// Server struct represents a backend server with its URL, status, reverse proxy, mutex for thread safety, and request count.
type Server struct {
	URL          *url.URL               // The URL of the backend server
	Alive        bool                   // Status indicating if the server is alive
	ReverseProxy *httputil.ReverseProxy // Reverse proxy to forward requests to this server
	Mutex        sync.RWMutex           // Mutex for read-write locking to ensure thread safety
	RequestCount int                    // Counter for the number of active requests to this server
}

// LoadBalancer struct contains a list of backend servers, the current index for round-robin, and a mutex for thread safety.
type LoadBalancer struct {
	Servers      []*Server  // Slice of pointers to Server structs
	CurrentIndex int        // Current index for round-robin selection
	Mutex        sync.Mutex // Mutex for ensuring thread safety when accessing shared resources
}

// NewLoadBalancer initializes a LoadBalancer with the given server URLs.
func NewLoadBalancer(serverUrls []string) *LoadBalancer {
	var servers []*Server
	for _, serverUrl := range serverUrls { // Iterate over the list of server URLs
		url, err := url.Parse(serverUrl) // Parse the server URL
		if err != nil {                  // Handle any URL parsing errors
			log.Fatal(err)
		}
		servers = append(servers, &Server{ // Append a new Server instance to the servers slice
			URL:          url,
			Alive:        true,                                    // Initialize the server as alive
			ReverseProxy: httputil.NewSingleHostReverseProxy(url), // Create a new reverse proxy for the server
		})
	}
	return &LoadBalancer{Servers: servers} // Return a pointer to the new LoadBalancer instance
}

// getNextServer selects the next available server using round-robin.
func (lb *LoadBalancer) getNextServer() *Server {
	lb.Mutex.Lock()         // Lock the mutex to ensure thread safety
	defer lb.Mutex.Unlock() // Unlock the mutex when the function returns

	for i := 0; i < len(lb.Servers); i++ { // Loop through the servers to find an alive one
		server := lb.Servers[lb.CurrentIndex]                     // Select the current server
		lb.CurrentIndex = (lb.CurrentIndex + 1) % len(lb.Servers) // Update the current index for the next request
		if server.Alive {                                         // Return the server if it is alive
			return server
		}
	}
	return nil // Return nil if no alive server is found
}

// getLeastConnectionServer selects the server with the least number of active connections.
func (lb *LoadBalancer) getLeastConnectionServer() *Server {
	lb.Mutex.Lock()         // Lock the mutex to ensure thread safety
	defer lb.Mutex.Unlock() // Unlock the mutex when the function returns

	minRequests := int(^uint(0) >> 1) // Set minRequests to the maximum integer value
	var leastConnServer *Server

	for _, server := range lb.Servers { // Loop through all servers
		server.Mutex.RLock() // Lock the server's mutex for reading
		if server.Alive && server.RequestCount < minRequests {
			minRequests = server.RequestCount // Update minRequests if the current server has fewer requests
			leastConnServer = server          // Update the leastConnServer to the current server
		}
		server.Mutex.RUnlock() // Unlock the server's mutex
	}
	return leastConnServer // Return the server with the least connections
}

// healthCheck performs periodic health checks on the backend servers.
func (lb *LoadBalancer) healthCheck() {
	ticker := time.NewTicker(10 * time.Second) // Create a new ticker that triggers every 10 seconds
	defer ticker.Stop()                        // Ensure the ticker is stopped when the function returns

	for range ticker.C { // Loop every time the ticker ticks
		for _, server := range lb.Servers { // Iterate over all servers
			go func(s *Server) { // Start a new goroutine for each server
				alive := isAlive(s.URL)                                    // Check if the server is alive
				s.Mutex.Lock()                                             // Lock the server's mutex for writing
				s.Alive = alive                                            // Update the server's alive status
				s.Mutex.Unlock()                                           // Unlock the server's mutex
				log.Printf("Health check for %s, alive: %t", s.URL, alive) // Log the result of the health check
			}(server)
		}
	}
}

// isAlive checks if a server is reachable by attempting a TCP connection.
func isAlive(u *url.URL) bool {
	conn, err := net.DialTimeout("tcp", u.Host, 5*time.Second) // Try to establish a TCP connection with a timeout
	if err != nil {                                            // If there is an error, the server is not alive
		return false
	}
	defer conn.Close() // Ensure the connection is closed
	return true        // Return true if the connection was successful
}

// ServeHTTP handles incoming HTTP requests and forwards them to the appropriate backend server.
func (lb *LoadBalancer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var server *Server
	for retries := 0; retries < len(lb.Servers); retries++ { // Attempt to find an available server
		server = lb.getNextServer() // Use round-robin to get the next server (could use getLeastConnectionServer instead)
		if server == nil {          // If no server is available, return a 503 error
			http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
			return
		}

		server.Mutex.Lock()   // Lock the server's mutex for writing
		server.RequestCount++ // Increment the request count
		server.Mutex.Unlock() // Unlock the server's mutex

		// Define an error handler for the reverse proxy
		proxyErrorHandler := func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("Error proxying to %s: %v", server.URL, err)
			server.Mutex.Lock()   // Lock the server's mutex for writing
			server.Alive = false  // Mark the server as not alive
			server.Mutex.Unlock() // Unlock the server's mutex
			lb.ServeHTTP(w, r)    // Retry with the next available server
		}

		server.ReverseProxy.ErrorHandler = proxyErrorHandler // Set the error handler for the reverse proxy
		server.ReverseProxy.ServeHTTP(w, r)                  // Serve the request using the reverse proxy

		server.Mutex.Lock()   // Lock the server's mutex for writing
		server.RequestCount-- // Decrement the request count
		server.Mutex.Unlock() // Unlock the server's mutex

		break // Break out of the retry loop if the request succeeded
	}
}

func main() {
	serverUrls := []string{
		"http://app1:5678",
		"http://app2:5678",
		"http://app3:5678",
		"http://app4:5678",
	}

	lb := NewLoadBalancer(serverUrls) // Initialize the load balancer with the given server URLs
	go lb.healthCheck()               // Start the health check in a separate goroutine

	http.HandleFunc("/", lb.ServeHTTP)             // Set the HTTP handler function for the load balancer
	log.Println("Starting load balancer on :8080") // Log that the load balancer is starting
	log.Fatal(http.ListenAndServe(":8080", nil))   // Start the HTTP server on port 8080 and log any fatal errors
}

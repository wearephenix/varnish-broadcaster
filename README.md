# Request distributor
Broadcasts requests to multiple [Varnish](<https://www.varnish-cache.org/>) caches from a single entry point.
The initial thought is to ease-up purging/banning across multiple [Varnish](<https://www.varnish-cache.org/>) cache instances.
The broadcaster consists out of a web-server which will handle the distribution of requests against all configured caches.

See [this](caches.json) file as an example on how to configure your caches.

Start the app with any of the following command line args:

  - **port**: The port under which the broadcaster is exposed. Defaults to **8080**
  - **goroutines**: Sets the number of available goroutines which will handle the broadcast against the caches. Defaults to a number of **2**, a higher number does not necesarilly imply a better performance. Can be tweaked though depending on the number of caches.
  
Required headers:

   - **X-Group**: Name of the group to broadcast against. Use ***all*** to broadcast against all caches.
   
Usage example:

Purge **/something/to/purge** in all caches within the Production group:
```
curl -Is http://localhost:8088/something/to/purge -H "X-Group: Production"  -X PURGE
```

Ban **/foo** in all caches within the Dev group:
```
curl -Is http://localhost:8088/foo -H "X-Group: Dev"  -X BAN
```

Purge everything in all your caches:
```
curl -Is http://localhost:8088/ -H "X-Group: all"  -X PURGE
```

Response example:
```
HTTP/1.1 200 OK
Date: Sat, 29 Oct 2016 15:20:19 GMT
Content-Length: 28
Content-Type: text/plain; charset=utf-8

Cache#1: HTTP/1.1 200
Cache#3: HTTP/1.1 200
Cache#4: HTTP/1.1 200
Cache#2: HTTP/1.0 501
```

Note that your VCL needs to be aware of your purging/banning intentions. See [here](https://www.varnish-cache.org/docs/trunk/users-guide/purging.html) for more cache invalidation details.

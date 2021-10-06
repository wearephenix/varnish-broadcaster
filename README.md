# Varnish broadcaster

Small tool that broadast requests to multiple [Varnish](https://www.varnish-cache.org/) server. Useful to purge/ban cache on a Varnish cluster with just one HTTP request.

## Build

Produce a build in `dist/varnish-broadcaster` :

```shell
make build
```

## Usage

See [this](caches.ini) file as an example on how to configure your caches.

Start the app with any of the following command line args:

- **port**: The port under which the broadcaster is exposed. Defaults to **8088**.
- **goroutines**: Sets the number of available goroutines which will handle the broadcast against the caches. Defaults to a number of **8**, a higher number does not necesarilly imply a better performance. Can be tweaked though depending on the number of caches.
- **cfg**: Path to an .ini file containing configured caches. This is a _required_ parameter.
- **retries**: Number of items to retry if a request fails to execute. Defaults to 1.
- **enforce**: If true, the response code will be set according to the first non-200 received from the Varnish nodes.
- **log-file**: Path to a log file. If none specified it defaults to `stdout`.
- **enable-log**: Switches logging on/off. Disabled by default.

### Optional headers

**X-Group**: Name of the group to broadcast against, if not used - the broadcast will be done against all caches.

### Configuration reload

If the broadcaster receives a `SIGHUP` notification, it will trigger a configuration reload from disk.

## Examples

Purge `/something/to/purge` in all Varnish servers :

```shell
curl -X PURGE http://localhost:8088/something/to/purge
```

Purge everything in all your servers :

```
curl -X PURGE http://localhost:8088/
```

Purge `/something/to/purge` in all caches within the `prod` group:

```shell
curl -X PURGE -H "X-Group: prod" http://localhost:8088/something/to/purge
```

## Credits

Project initially developed by [Marius Magureanu](https://github.com/mariusmagureanu), then maintained by [Guillaume Quintard](https://github.com/gquintard/broadcaster). Few commits are also inspired by [Timothy Clarke's fork](https://github.com/timothyclarke/http-request-broadcaster).

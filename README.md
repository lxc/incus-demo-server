# Incus demo server

This repository contains the backend code of the Incus online demo service.

[https://linuxcontainers.org/incus/try-it](https://linuxcontainers.org/incus/try-it)

## What is it

Simply put, it's a small Go daemon exposing a REST API that users
(mostly our javascript client) can interact with to create temporary
test instances and attach to that instance's console.

Those instances come with a bunch of resource limitations and an
expiry, when the instance expires, it's automatically deleted.

The main client can be found at the URL above, with its source available here:  
[https://github.com/lxc/linuxcontainers.org](https://github.com/lxc/linuxcontainers.org)

## Dependencies

The server needs to be able to talk to an Incus daemon over the local unix
socket or a remote HTTPS connection, so you need to have a Incus daemon
installed and functional before using this server.

Other than that, you can build the daemon with:

    go install github.com/lxc/incus-demo-server/cmd/incus-demo-server@latest

## Running it

To run your own, you should start by copying the example configuration
file "config.yaml.example" to "config.yaml", then update its content
according to your environment.

You will either need an instance to copy for every request or an
instance image to use, set that up and set the appropriate
configuration key.

Once done, simply run the daemon with:

    ./incus-demo-server

The daemon isn't verbose at all, in fact it will only log critical Incus errors.

You can test things with:

    curl http://localhost:8080/1.0
    curl http://localhost:8080/1.0/terms

The server monitors the current directory for changes to its configuration file.
It will automatically reload the configuration after it's changed.

## Bug reports

Bug reports can be filed at https://github.com/lxc/incus-demo-server/issues/new

## Contributing

Fixes and new features are greatly appreciated but please read our
[contributing guidelines](CONTRIBUTING.md) first.

Contributions to this project should be sent as pull requests on github.

## Support and discussions

We use the LXC mailing-lists for developer and user discussions, you can
find and subscribe to those at: https://lists.linuxcontainers.org

If you prefer live discussions, some of us also hang out in
[#lxc](https://web.libera.chat/#lxc) on libera.chat.

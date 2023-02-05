# rsslay

[![CI Build & Test](https://github.com/piraces/rsslay/actions/workflows/main.yml/badge.svg)](https://github.com/piraces/rsslay/actions/workflows/main.yml)
[![Fly Deploy](https://github.com/piraces/rsslay/actions/workflows/fly.yml/badge.svg)](https://github.com/piraces/rsslay/actions/workflows/fly.yml)
[![CI Dive Check](https://github.com/piraces/rsslay/actions/workflows/dive-check.yml/badge.svg)](https://github.com/piraces/rsslay/actions/workflows/dive-check.yml)
[![Publish Docker image](https://github.com/piraces/rsslay/actions/workflows/docker-publish.yml/badge.svg)](https://github.com/piraces/rsslay/actions/workflows/docker-publish.yml)
[![codecov](https://codecov.io/gh/piraces/rsslay/branch/main/graph/badge.svg?token=tNKcOjlxLo)](https://codecov.io/gh/piraces/rsslay)

![Docker Hub](https://img.shields.io/docker/pulls/piraces/rsslay?logo=docker)
[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2Fpiraces%2Frsslay.svg?type=shield)](https://app.fossa.com/projects/git%2Bgithub.com%2Fpiraces%2Frsslay?ref=badge_shield)

[![Go Report Card](https://goreportcard.com/badge/github.com/piraces/rsslay)](https://goreportcard.com/report/github.com/piraces/rsslay)
[![Go Reference](https://pkg.go.dev/badge/github.com/piraces/rsslay.svg)](https://pkg.go.dev/github.com/piraces/rsslay)

**Relay that creates virtual nostr profiles for each RSS feed submitted**

**Donate for development ⚡:** [https://getalby.com/p/piraces](https://getalby.com/p/piraces)

**Working relay: `wss://rsslay.nostr.moe`. Frontend available in [rsslay.nostr.moe](https://rsslay.nostr.moe).**

  - A Nostr relay implementation based on [relayer](https://github.com/fiatjaf/relayer/) by [fiatjaf](https://fiatjaf.com).
  - Doesn't accept any events, only emits them.
  - Does so by manually reading and parsing RSS feeds.

[![Deploy on Railway](https://railway.app/button.svg)](https://railway.app/new/template/UDf6vC?referralCode=Zbo_gO)

![Screenshot of main page](screenshot.png)

## API

`rsslay` exposes an API to work with it programmatically, so you can automate feed creation and retrieval.
The following operations are available:

### Get/Create a feed

- Path: `/api/feed`
- Method: `GET` or `POST`
- Query params **(mandatory)**: `url`
- Example: `GET https://rsslay.nostr.moe/api/feed?url=https://www.engadget.com/rss.xml`
- Result: `application/json`
- Example result: 
   ```json
   {
     "PubKey": "1e630062dd55226058224a0a1e9b54e09ac121ed13dd5070758816a9c561aeab",
     "NPubKey": "npub1re3sqcka253xqkpzfg9pax65uzdvzg0dz0w4qur43qt2n3tp464sswsn92",
     "Url": "https://www.engadget.com/rss.xml",
     "Error": false,
     "ErrorMessage": "", 
     "ErrorCode": 0
   }
   ```

Example with cURL:
```shell
curl --location --request GET \ 
'https://rsslay.nostr.moe/api/feed?url=https://nitter.moomoo.me/suhail/rss'
```

## Running the relay from the source

1. Clone this repository (or fork it).
2. Set the `SECRET` environment variable (a random string to be used to generate virtual private keys).
3. Set the following flags (may differ per environment):
    ```shell
    export CGO_ENABLED=1
    export GOARCH=amd64
    export GOOS=linux
    ```
4. Proceed to build the binary with the following command:
    ```shell
    go build -ldflags="-s -w -linkmode external -extldflags '-static'" -o ./rsslay
    ```

5. Run the relay!
    ```shell
    ./rsslay
    ```

_**Note:** it will create a local database file to store the currently known RSS feed URLs._

### Environment variables used
- `SECRET`: **mandatory**, a random string to be used to generate virtual private keys.
- `DB_DIR`: path with filename where the database should be created, defaults to `.\db\rsslay.sqlite`.
- `DEFAULT_PROFILE_PICTURE_URL`: default profile picture URL for feeds that don't have an image.
- `REPLAY_TO_RELAYS`: set to true if you want to send the fetched events to other relays defined in `RELAYS_TO_PUBLISH_TO` (default is false)
- `RELAYS_TO_PUBLISH_TO`: string with relays separated by `,` to re-publish events to in format `wss://[URL],wss://[URL2]` where `URL` and `URL2` are URLs of valid relays (default is empty)
- `DEFAULT_WAIT_TIME_BETWEEN_BATCHES`: default time to wait between sending batches of requests to other relays in milliseconds (default 60000, 60 seconds)
- `DEFAULT_WAIT_TIME_FOR_RELAY_RESPONSE`: default time to wait for relay response for possible auth event in milliseconds (default is 3000, 3 seconds).
- `MAX_EVENTS_TO_REPLAY`: maximum number of events to send to a relay in `RELAYS_TO_PUBLISH_TO` in a batch
- `ENABLE_AUTO_NIP05_REGISTRATION`: enables [NIP-05](https://github.com/nostr-protocol/nips/blob/master/05.md) automatic registration for all feed profiles in the format `[URL]@[MAIN_DOMAIN_NAME]` where URL is the main URL for the feed and `MAIN_DOMAIN_NAME` the below environment variable (default `false`)
- `MAIN_DOMAIN_NAME`: main domain name where this relay will be available (only for NIP-05 purposes if enabled with `ENABLE_AUTO_NIP05_REGISTRATION`)
- `OWNER_PUBLIC_KEY`: public key to show at the `/.well-known/nostr.json` endpoint by default mapped as the domain owner (`_@[MAIN_DOMAIN_NAME]` where `MAIN_DOMAIN_NAME` is the environment variable)
- `MAX_SUBROUTINES`: maximum number to maintain in order to replay events to other relays (to prevent crash, default 20)

## Running with Docker

The Docker image for this project is published in [GitHub Packages](https://github.com/piraces/rsslay/pkgs/container/rsslay) and [Docker Hub](https://hub.docker.com/r/piraces/rsslay), so you can directly
pull the image from that feeds.

Nevertheless, you can always use the git repository and its source code to build and run it by yourself.

### From the published releases

1. Pull the image from GitHub or Docker Hub (both are the same):
   ```shell
   # From GitHub (you can change the tag to a specific version)
   docker pull ghcr.io/piraces/rsslay:latest
   ```
   ```shell
   # From DockerHub (you can change the tag to a specific version)
   docker pull piraces/rsslay:latest
   ```
2. Copy the `.env.sample` file to `.env` and replace the variable values as needed.
3. Run the final image!
   ```shell
   # This will run in "detached" mode exposing the port 8080, change the port as needed
   # In case you downloaded the image from DockerHub
   docker run -d --env-file .env -p 8080:8080 --name rsslay piraces/rsslay:latest
   # If you downloaded the image from GitHub
   docker run -d --env-file .env -p 8080:8080 --name rsslay ghcr.io/piraces/rsslay:latest
   ```
4. Now you can access the instance in `localhost:8080` (or other port you choose).

### Directly from the repository

_**Note:** you can skip step 2 and 3 from below and directly go and run:_
```shell
docker build github.com/piraces/rsslay -t rsslay
```

1. Make sure you have already installed [Docker](https://docs.docker.com/engine/install/).
2. Clone this repository (or fork it).
3. Perform a docker build:
   ```shell
   docker build . -t rsslay
   ```
4. Copy the `.env.sample` file to `.env` and replace the variable values as needed.
5. Run the final image!
   ```shell
   # This will run in "detached" mode exposing the port 8080, change the port as needed
   docker run -d --env-file .env -p 8080:8080 --name rsslay rsslay
   ```
6. Now you can access the instance in `localhost:8080` (or other port you choose).

## Deploying easily with [litefs](https://fly.io/docs/litefs/getting-started/) to [fly.io](https://fly.io/)

I'm currently deploying an instance of rsslay into [fly.io](https://fly.io/), so I made it simple to 
everyone to deploy to there.

The requisites are the following:
- An account in fly.io.
- An app instance previously created.
- A volume to handle the database files between deployments, restarts and others.
- (Optional) a custom domain of our own that we can set a CNAME record to and avoid using the default domain.

### Setting up the app

1. Download the [flyctl CLI](https://fly.io/docs/hands-on/install-flyctl/).
2. Login into your account with `flyctl auth login`.
3. Modify the file `fly.toml` replacing the property `app` with your app name.
4. Create a new volume for your app with `flyctl volumes create rsslay_data` (the name `rsslay_data` can be changed).
5. Modify the file `fly.toml` and set the section `[mounts]` accordingly (the `source` property with the volume name and **keep `destination` as it is due to LiteFS usage**).
6. Create a secret with `flyctl secrets set SECRET=YOUR_LONG_STRING_HERE`, in order to establish the `SECRET` environment variable to create private keys with.
7. Proceed with the automatic deployment with `flyctl launch`
8. **Optional:** set up a CNAME record and [set a certificate for the app](https://fly.io/docs/app-guides/custom-domains-with-fly/#creating-a-custom-domain-on-fly-manually).
9. **Optional:** [set up a workflow in GitHub to automatically deploy your app](https://fly.io/docs/app-guides/continuous-deployment-with-github-actions/) like in this repo.

You are done! And you can scale your app simply following [this steps](https://fly.io/docs/litefs/example/#scaling-up-your-app)!

# Contributing

Feel free to [open an issue](https://github.com/piraces/rsslay/issues/new), provide feedback in [discussions](https://github.com/piraces/rsslay/discussions), or fork the repo and open a PR with your contribution!

**All kinds of contributions are welcome!**

# Contact

You can reach me on nostr [`npub1ftpy6thgy2354xypal6jd0m37wtsgsvcxljvzje5vskc9cg3a5usexrrtq`](https://snort.social/p/npub1ftpy6thgy2354xypal6jd0m37wtsgsvcxljvzje5vskc9cg3a5usexrrtq)

Also on [the bird site](https://twitter.com/piraces_), and [Mastodon](https://hachyderm.io/@piraces).

# License

[Unlicense](https://unlicense.org).

[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2Fpiraces%2Frsslay.svg?type=large)](https://app.fossa.com/projects/git%2Bgithub.com%2Fpiraces%2Frsslay?ref=badge_large)


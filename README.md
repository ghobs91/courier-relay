# rsslay

[![Fly Deploy](https://github.com/piraces/rsslay/actions/workflows/fly.yml/badge.svg)](https://github.com/piraces/rsslay/actions/workflows/fly.yml)

**Relay that creates virtual nostr profiles for each RSS feed submitted**

**Working demo: `wss://rsslay.nostr.moe`. Frontend available in [rsslay.nostr.moe](https://rsslay.nostr.moe).**

  - A Nostr relay implementation based on [relayer](https://github.com/fiatjaf/relayer/) by [fiatjaf](https://fiatjaf.com).
  - Doesn't accept any events, only emits them.
  - Does so by manually reading and parsing RSS feeds.

![Screenshot of main page](screenshot.png)

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
- `DB_DIR`: directory where the database should be created, defaults to `.\db`.
- `DEFAULT_PROFILE_PICTURE_URL`: default profile picture URL for feeds that don't have an image.

## Deploying easily to [fly.io](https://fly.io/)

I'm currently deploying a little instance of rsslay into [fly.io](https://fly.io/), so I made it simple to 
everyone to deploy to there.

The requisites are the following:
- An account in fly.io.
- An app instance previously created.
- A little volume to handle the database files between deployments, restarts and others.
- (Optional) a custom domain of our own that we can set a CNAME record to and avoid using the default domain.

### Setting up the app

1. Download the [flyctl CLI](https://fly.io/docs/hands-on/install-flyctl/).
2. Login into your account with `flyctl auth login`.
3. Modify the file `fly.toml` replacing the property `app` with your app name.
4. Create a new volume for your app with `flyctl volumes create rsslay_data` (the name `rsslay_data` can be changed).
5. Modify the file `fly.toml` and set the section `[mounts]` accordingly (the `source` property with the volume name and the `destination` with where do you want it to be mounted).
6. Create a secret with `flyctl secrets set SECRET=YOUR_LONG_STRING_HERE`, in order to establish the `SECRET` environment variable to create private keys with.
7. In the `[env]` section set `DB_DIR` to the folder you mounted the volume to.
8. Proceed with the automatic deployment with `flyctl launch`
9. **Optional:** set up a CNAME record and [set a certificate for the app](https://fly.io/docs/app-guides/custom-domains-with-fly/#creating-a-custom-domain-on-fly-manually).
10. **Optional:** [set up a workflow in GitHub to automatically deploy your app](https://fly.io/docs/app-guides/continuous-deployment-with-github-actions/) like in this repo.

You are done!

# License

[Unlicense](https://unlicense.org).


# Browser cookies

Agents' browsers start signed out. To let them use sites you're signed in to (a staging app, a
dashboard, GitHub), import cookies you export from your own browser into a project: the agents
created afterwards start their Chromium with them.

AgentBox never reads your browser's files. You make the export, you pick the sites.

## Importing

1. Export your cookies with an extension: **Get cookies.txt LOCALLY** (Netscape `cookies.txt`), or
   **Cookie-Editor** / **EditThisCookie** (JSON). A Playwright `storageState` file works too.
2. In the app, open the project's **Settings → Secrets**, and under **Browser cookies** choose
   **Import cookies…**. Paste the export or choose the file.
3. Tick the sites agents should be signed in to, or type domains (`github.com` covers its
   subdomains), and import.

From the command line:

```bash
agentbox browser-cookies import pawly cookies.txt                       # lists the sites in it
agentbox browser-cookies import pawly cookies.txt --domain github.com   # imports those
agentbox browser-cookies status pawly
agentbox browser-cookies remove pawly
```

## What happens to them

- Only the cookies of the domains you picked are kept, sealed like a project secret. No page or
  command shows a cookie again: only the domains and how many.
- An agent created after the import gets them in its Chromium the first time its browser starts.
  Agents created before don't. They aren't environment variables, and agents aren't told their values.
- Importing again replaces the cookies; **Remove** forgets them. Agents that already have them keep
  them until they're destroyed.
- Expired cookies are dropped at import. When a site signs the agents out, export and import again.

Anyone who can use the project's agents is signed in as you on those sites: import what an agent
needs, not your whole browser.

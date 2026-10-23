# Mail

Flock includes an SMTP server that catches every email your apps send. Nothing is delivered to real inboxes, so you can safely test password resets, notifications and newsletters.

## Configuring Laravel

```bash
MAIL_MAILER=smtp
MAIL_HOST=127.0.0.1
MAIL_PORT=2525
MAIL_USERNAME="${APP_NAME}"
MAIL_PASSWORD=null
MAIL_SCHEME=null
```

:::app
**Settings → Mail** shows these lines under **Environment Variables**, with a copy button.
:::

The catcher accepts any credentials. The **username picks the inbox**, so with `MAIL_USERNAME="${APP_NAME}"` every app gets its own inbox and messages from different projects don't mix. Mail sent without a username lands in the `default` inbox.

Messages up to 25 MB are accepted, attachments included.

## Reading mail

:::app
Click **Mail** in the Flock panel, or **Open Mail** in **Settings → Mail**. On Windows, it's also in the tray icon's right-click menu.

![The Mail window](screenshot:mail-window)

The list shows each message's sender, subject, inbox and time. Select one to see its recipients and attachments, and switch between the **HTML**, **Text**, **Raw** and **Headers** views. Click an attachment (it shows its name and size) to save it.

**Clear All** deletes every caught message.
:::

:::cli
```bash
flock mail                                   # list caught emails
flock mail open 1760790000000000000-000001   # open a message in your system's mail app
flock mail clear                             # delete all messages
flock mail clear acme                        # empty a single inbox
```

Copy a message's ID from the `ID` column of `flock mail`. Inbox names are lowercase, so `MAIL_USERNAME=Acme` lands in `acme`.
:::

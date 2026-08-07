# App Icons

This folder should contain the app icons for PWA installation and Android APK.

## Required Icons

- `icon-192.png` — 192x192 pixels (for Android PWA)
- `icon-512.png` — 512x512 pixels (for high-res displays)

## Where to Get Icons

### Free Icon Sources
- **Flaticon** — https://www.flaticon.com (search: "chat phone call")
- **IconFinder** — https://www.iconfinder.com (search: "chat app")
- **Icons8** — https://icons8.com (search: "communication")
- **Freepik** — https://www.freepik.com (search: "chat app icon")

### Recommended Style
- Flat or minimal design
- Dark theme compatible (use colors that work on dark backgrounds)
- Phone + chat bubble combination to represent calling + messaging
- Red/pink accent color (#e94560) to match app theme

## How to Add Icons

1. Download icons from the sources above
2. Rename them to `icon-192.png` and `icon-512.png`
3. Place them in this `icons/` folder
4. The app will automatically use them

## For Android APK

To use these icons in the Android app:
```bash
cp icons/icon-192.png android/app/src/main/res/mipmap-hdpi/ic_launcher.png
cp icons/icon-512.png android/app/src/main/res/mipmap-xhdpi/ic_launcher.png
```

Then build the APK as described in `android/README.md`.

<p align="center">
  <img src="web/icon.png" width="96" height="96" alt="Icône GhostCam">
</p>

<h1 align="center">GhostCam</h1>

<p align="center">
  <a href="README.md">English</a> · <b>Français</b>
</p>

<p align="center">
  <sub>Fait avec ❤️ en France 🇫🇷</sub>
</p>

<p align="center">
  <b>Votre téléphone filme, votre PC garde la preuve.</b><br>
  Envoyez la caméra d'un téléphone vers votre propre ordinateur, où qu'il soit (4G/5G, n'importe quel Wi-Fi), avec écriture sur le disque en temps réel.
  Rien n'est jamais stocké sur le téléphone.
</p>

<p align="center">
  <img src="docs/screenshots/phone-recording.png" height="420" alt="Téléphone en enregistrement">
  &nbsp;&nbsp;
  <img src="docs/screenshots/pc-recording.png" height="420" alt="Fenêtre PC pendant l'enregistrement">
</p>

---

## Pourquoi GhostCam ?

Si un téléphone est confisqué, cassé ou éteint, ce qu'il a enregistré en local
peut être perdu. GhostCam en fait une **caméra distante sans stockage local** :
chaque image part directement vers un PC que vous contrôlez, et chaque seconde
reçue est déjà sur son disque.

- **Rien à installer sur le téléphone.** On scanne un QR code et la page caméra s'ouvre dans Safari (iOS) ou Chrome (Android).
- **Fonctionne entre réseaux différents.** Téléphone en 4G/5G dehors, PC à la maison derrière sa box : pas de redirection de port, pas de compte, pas de serveur à louer.
- **Résiste à une coupure brutale.** Les fichiers sont écrits au fil de l'eau et restent lisibles si le téléphone disparaît en pleine vidéo.
- **Chiffré de bout en bout.** Ni le tunnel ni aucun relais ne peut lire la vidéo, injecter des images, ni démarrer ou arrêter un enregistrement.
- **Un seul fichier, léger.** Un exécutable Windows d'environ 13 Mo écrit en Go. `cloudflared` est téléchargé, vérifié et mis à jour automatiquement. L'interface est une fenêtre native WebView2.
- **Open source**, donc auditable, ce qui est indispensable pour un outil de ce genre.

## Fonctionnalités

| Sur le téléphone | Sur le PC |
|---|---|
| Un bouton pour démarrer et arrêter, autant de vidéos que vous voulez | Un fichier par vidéo, chrono en direct, débit reçu |
| Aperçu en direct, rien n'est envoyé entre deux vidéos (économise données et batterie) | Préréglages de qualité avec avertissements sur la latence |
| Reconnexion automatique : si le réseau coupe en pleine vidéo, l'enregistrement reprend dans un nouveau fichier | Choix du dossier d'enregistrement (sélecteur de dossier natif) |
| Écran maintenu allumé (Wake Lock) | Appairage par QR code ou « Copier le lien » |
| État clair : connexion directe, relais chiffré, réseau lent | Fonctionne aussi sans fenêtre (`-headless`, QR code dans le terminal) |

<p align="center">
  <img src="docs/screenshots/pc-pairing.png" height="380" alt="Appairage">
  &nbsp;
  <img src="docs/screenshots/pc-settings.png" height="380" alt="Paramètres">
  &nbsp;
  <img src="docs/screenshots/phone-intro.png" height="380" alt="Accueil sur le téléphone">
</p>

## Démarrage rapide (Windows)

1. Compilez `dist/ghostcam.exe` (voir [Compilation](#compilation)). C'est le seul fichier nécessaire.
2. Lancez-le et acceptez quand le pare-feu Windows le demande : c'est ce qui autorise les connexions UDP directes.
   Au premier lancement, il télécharge `cloudflared` (environ 50 Mo, voir [Binaire du tunnel](#binaire-du-tunnel-cloudflared)) : une connexion Internet est donc nécessaire une fois.
3. Scannez le QR code avec le téléphone, appuyez sur **Activer la caméra**, puis sur le bouton rouge.
4. Les vidéos sont enregistrées par défaut dans `Vidéos\GhostCam`. Le dossier et la qualité se changent avec le bouton Paramètres (en haut à droite).

WebView2 est préinstallé sur Windows 10/11. S'il manque, GhostCam ouvre la même
page dans votre navigateur par défaut.

## Fonctionnement

```
 Téléphone (4G, CGNAT)                     Internet                       PC à la maison (derrière la box)
 ┌──────────────┐  HTTPS/WSS  ┌──────────────────────────┐  QUIC sortant   ┌─────────────────────┐
 │ Page web     │────────────▶│ Cloudflare Quick Tunnel  │◀───────────────│ cloudflared (enfant)│
 │ getUserMedia │ signaling   │ https://xxx.trycloudflare│                 │ ghostcam            │
 │ RTCPeerConn. │             └──────────────────────────┘                 │  :8080 public (lo)  │
 │              │                                                          │  :8081 admin  (lo)  │
 │              │════════ Média WebRTC, DTLS-SRTP, UDP ═══════════════════▶│  :50000/udp (ICE)   │
 └──────────────┘   1. IP publique:50000 ouverte par UPnP (direct)         │  → *.webm sur disque│
                    2. Perçage du NAT par STUN (direct)                    └─────────────────────┘
                    3. Relais TURN (facultatif, votre serveur)
                    4. Secours : morceaux MediaRecorder chiffrés AES-GCM, via le tunnel WSS
```

- **Le signaling et la page du téléphone** passent par un Cloudflare Quick Tunnel
  que l'app lance elle-même. `cloudflared` n'établit que des connexions sortantes,
  et le téléphone obtient une URL HTTPS valide, que les navigateurs exigent pour
  accéder à la caméra.
- **La vidéo** passe en WebRTC sur un seul port UDP fixe. L'app demande à la box
  d'ouvrir ce port par **UPnP** et transmet son adresse publique au téléphone
  comme candidat ICE supplémentaire : le téléphone peut ainsi joindre le PC
  directement, même derrière le NAT d'un opérateur mobile. Sans UPnP, le perçage
  du NAT par STUN fonctionne souvent. Un serveur TURN facultatif couvre les cas
  restants.
- **Secours** : si aucun chemin WebRTC ne s'établit en environ 12 s, le téléphone
  bascule sur des morceaux `MediaRecorder` envoyés chaque seconde par le même
  WebSocket, chiffrés de bout en bout. Ce chemin fonctionne dès que la page se
  charge.
- **Appairage et enregistrement** : un appairage garde une connexion ouverte.
  Chaque Rec/Stop produit un fichier. Les commandes de démarrage et d'arrêt
  passent par un DataChannel WebRTC, ou à l'intérieur des trames chiffrées du
  relais.

## Binaire du tunnel (cloudflared)

GhostCam ne livre pas `cloudflared`. À chaque lancement, il consulte les
releases GitHub officielles de Cloudflare et installe ou met à jour sa propre
copie dans `%LOCALAPPDATA%\GhostCam\bin\` :

- Un téléchargement n'est accepté que si son **SHA-256** correspond à
  l'empreinte publiée par GitHub pour ce fichier (et à celle que Cloudflare
  liste dans ses notes de version, quand elle y figure), et s'il porte une
  **signature Authenticode valide de « Cloudflare, Inc. »**. Ce contrôle de
  signature ne dépend pas de GitHub.
- À chaque lancement, l'empreinte du binaire installé est recalculée. S'il a été
  modifié, il est réinstallé.
- **Hors ligne**, la copie installée est utilisée telle quelle. Si la mise à
  jour échoue, la version précédente est conservée.
- `cloudflared` tourne dans un Job Object Windows : il s'arrête avec GhostCam,
  même en cas de crash ou d'arrêt forcé.
- Le QR code n'apparaît qu'une fois le nom du tunnel visible dans le DNS public
  (vérifié auprès de 1.1.1.1, pour ne pas polluer le cache DNS de la box).

`-cloudflared <chemin>` court-circuite tout cela et utilise votre propre binaire.

## Modèle de sécurité

| Menace | Protection |
|---|---|
| Quelqu'un d'autre se connecte | Secret de 256 bits dans le **fragment** de l'URL du QR code (`#t=`). Les navigateurs n'envoient jamais le fragment sur le réseau. Le téléphone prouve qu'il connaît le secret par un HMAC(nonce du serveur, offre SDP). |
| Le tunnel ou le TURN se fait passer pour le PC | L'empreinte du certificat DTLS du PC est épinglée dans le QR code (`&f=`), et le téléphone rejette toute autre réponse. |
| Le tunnel lit ou modifie la vidéo | Le média WebRTC est en DTLS-SRTP de bout en bout. Les trames du relais, trames de contrôle comprises, sont chiffrées en AES-256-GCM avec AAD = nonce‖seq : impossible de les rejouer, de les réordonner ou d'en supprimer sans que ce soit détecté. |
| Un site web lit le secret depuis localhost | L'interface admin n'écoute qu'en local, n'est jamais exposée par le tunnel, vérifie l'en-tête Host (DNS rebinding) et exige un en-tête spécifique pour toute action qui modifie quelque chose. |
| WebSocket intersite ou injection de script | L'Origin doit correspondre au Host, et la page du téléphone a une CSP stricte. |

**Limite connue :** c'est le tunnel qui sert la page du téléphone, donc un
opérateur de tunnel malveillant pourrait servir un JavaScript modifié. Pour
supprimer cette confiance, il faudrait héberger `web/` sur une origine statique
que vous contrôlez et n'utiliser le tunnel que pour `/ws`. Contributions
bienvenues.

## Format d'enregistrement

- **Direct (WebRTC) :** VP8 + Opus en **WebM avec Segment et Clusters de taille
  inconnue**, écrit à l'arrivée des paquets et synchronisé sur le disque chaque
  seconde. Une coupure brutale laisse un fichier lisible (VLC, mpv, ffmpeg,
  navigateurs).
- **Relais :** le flux MediaRecorder du téléphone lui-même : WebM sur
  Chrome/Android, WebM ou MP4 sur Safari selon la version. Le WebM tolère les
  coupures. Après une coupure brutale, un MP4 Safari peut nécessiter
  `ffmpeg -i in.mp4 -c copy out.mp4`.
- Les fichiers sont écrits en direct, donc sans index de navigation. Pour pouvoir
  avancer facilement dans la vidéo : `ffmpeg -i in.webm -c copy fixed.webm`.
- Noms : `ghostcam-<date>-<webrtc|relay>.<ext>`.

### Préréglages de qualité

Ils se règlent dans le panneau Paramètres, sont enregistrés dans
`%LOCALAPPDATA%\GhostCam\settings.json`, et s'appliquent dès la vidéo suivante
sans reconnecter le téléphone.

| Préréglage | Caméra | Débit montant nécessaire | Disque |
|---|---|---|---|
| Économie | 480p 24 i/s | ≈ 0,8 Mbit/s | ≈ 0,3 Go/h |
| Standard (par défaut) | 720p 30 i/s | ≈ 2 Mbit/s | ≈ 0,8 Go/h |
| Haute | 1080p 30 i/s | ≈ 4 Mbit/s | ≈ 1,7 Go/h |
| Maximale | 1080p 30 i/s | ≈ 8 Mbit/s | ≈ 3,4 Go/h |

Si le débit montant du téléphone est inférieur à ce que demande le préréglage :
- **Direct :** la vidéo reste en temps réel mais perd des images (`degradationPreference: maintain-resolution`).
- **Relais :** les données s'accumulent sur le téléphone, ce qui veut dire que ces secondes ne sont **pas encore en sécurité sur le PC**.

La fenêtre du PC affiche le débit reçu et prévient quand il tombe sous la moitié
de la cible.

## Compilation

Tout se compile dans Docker. Il suffit d'avoir Docker et Docker Compose sur la machine.

```sh
git clone https://github.com/p3374/GhostCam.git && cd GhostCam
docker compose run --rm build-windows   # -> dist/ghostcam.exe (un seul fichier, licences intégrées)
docker compose up dev                   # dev dans un conteneur : QR code dans les logs, admin sur http://localhost:8081
docker compose run --rm icons           # régénère les icônes depuis web/icon.svg (PNG + ressource Windows .syso)
```

Lancez le `.exe` directement sous Windows : UPnP et l'UDP direct ont besoin du
réseau de la machine. Dans Docker, seul le relais chiffré fonctionne (et parfois
STUN).

### Options de ligne de commande

| Option | Défaut | Rôle |
|---|---|---|
| `-out <dossier>` | réglage enregistré | Dossier des vidéos (remplace le réglage) |
| `-rtc-port <n>` | `50000` | Port UDP pour WebRTC (ouvert par UPnP) |
| `-no-upnp` | désactivé | Ne pas toucher à la box |
| `-public-url <url-https>` | Quick Tunnel | Utiliser votre propre reverse proxy ou un tunnel nommé à la place |
| `-cloudflared <chemin>` | téléchargé et mis à jour automatiquement | Utiliser ce binaire cloudflared à la place |
| `-stun <urls>` | Cloudflare + Google | Serveurs STUN séparés par des virgules ; vide = STUN désactivé |
| `-turn <url> -turn-user <u>` | aucun | Serveur TURN ; mot de passe via `GHOSTCAM_TURN_PASS` |
| `-headless` | désactivé | Pas de fenêtre : affiche le QR code dans le terminal |
| `-web-dir <dossier>` | intégré | Sert `web/` depuis le disque (modification en direct) |

Les logs sont écrits dans `%LOCALAPPDATA%\GhostCam\ghostcam.log`.

## Tests

Un test de bout en bout utilise un Chromium sans interface, avec une fausse
caméra, dans le rôle du téléphone. Il enregistre 3 vidéos, change la qualité et
le dossier en cours de session, puis tue le navigateur en pleine vidéo
(téléphone « détruit »). Il ne réussit que si ffmpeg arrive à décoder chaque
fichier.

```sh
docker compose run --rm build-linux
MODE=webrtc docker compose run --rm e2e                   # chemin direct
MODE=relay  docker compose run --rm e2e                   # UDP bloqué : relais chiffré
MODE=webrtc docker compose run --rm -e QUALITY2=high e2e  # qualité en hausse plutôt qu'en baisse
```

Pour chaque fichier, le test affiche le nombre d'images et la résolution de la
première et de la dernière image.

## Organisation du projet

```
cmd/ghostcam/        point d'entrée : options, tunnel, UPnP, réglages ; fenêtre native + sélecteur de dossier (gui_windows.go)
internal/server/     HTTP (public + admin), signaling WebSocket, session WebRTC, relais chiffré, réglages
internal/record/     fichier en ajout seul synchronisé sur disque, multiplexeur WebM en direct
internal/pairing/    secret d'appairage, authentification HMAC, déchiffrement AES-GCM des trames
internal/tunnel/     Cloudflare Quick Tunnel
internal/upnp/       ouverture du port sur la box + renouvellement du bail
web/                 page du téléphone (index.html, app.js), fenêtre PC (admin.html), icône (intégrées au binaire)
notices.go           intègre LICENSE + third_party_licenses.txt (affichés dans l'app)
scripts/             gen-notices.sh : régénère third_party_licenses.txt
test/e2e/            test de bout en bout Playwright
```

## Problèmes connus et feuille de route

Bons points d'entrée pour contribuer :

- **Échec occasionnel de la connexion directe.** Lors de tests avec de nombreuses reconnexions rapides, environ 1 tentative sur 8 n'a pas réussi en direct et a basculé sur le relais après environ 12 s. Cause pas encore identifiée.
- **iOS en arrière-plan.** iOS coupe la caméra quand Safari passe en arrière-plan. La page se reconnecte avec une caméra relancée à son retour, mais ce n'est pas encore testé sur un vrai iPhone.
- **Résolution en mode relais.** La baisse de qualité est confirmée en mode direct. En mode relais, avec la fausse caméra de Chromium, MediaRecorder a gardé la résolution native. C'est à vérifier sur de vrais téléphones.
- **Page du téléphone auto-hébergée**, pour ne plus avoir à faire confiance au tunnel pour le JavaScript (voir le Modèle de sécurité).
- **Fenêtre native sur macOS et Linux.** Ces plateformes utilisent pour l'instant le navigateur.
- **Tests unitaires et CI.** Il n'y a aujourd'hui que le test de bout en bout.
- **Traduction de l'interface.** Elle n'existe qu'en français.
- **ICE restart**, plutôt qu'un nouveau fichier lors des coupures réseau courtes.
- **Index de navigation à l'arrêt**, par remultiplexage pour ajouter des Cues et permettre aux lecteurs d'avancer dans la vidéo.

## Contribuer

Les contributions sont les bienvenues : les retours d'expérience sur de vrais
réseaux (opérateur, modèle de box, téléphone et navigateur) valent autant que du
code. Ouvrez une [issue](https://github.com/p3374/GhostCam/issues) ou une pull
request, et consultez [CONTRIBUTING.md](CONTRIBUTING.md) (en anglais).

## Usage responsable

GhostCam enregistre la vidéo et le son. Les lois sur le fait de filmer des
personnes, d'enregistrer du son et d'utiliser des enregistrements comme preuve
varient selon les pays. Vous êtes responsable d'en faire un usage légal et de
respecter la vie privée d'autrui.

## Licence

GhostCam est un logiciel libre, sous licence
[GNU Affero General Public License v3.0 ou ultérieure](LICENSE) (`AGPL-3.0-or-later`).

En résumé :
- Vous pouvez l'utiliser, l'étudier, le modifier et le partager, commercialement ou non.
- Si vous **distribuez** une version modifiée (binaires ou sources), vous devez
  en rendre le code source complet disponible sous la même licence.
- Si vous laissez **d'autres personnes utiliser une version modifiée par le
  réseau**, par exemple en l'hébergeant comme service, vous devez aussi leur en
  proposer le code source (section 13 de l'AGPL).
- Les modifications privées, que vous ne distribuez pas et n'exposez à personne,
  n'entraînent aucune obligation.

Ce résumé n'est pas un avis juridique : seul le texte de la [LICENSE](LICENSE)
fait foi.

Les composants tiers gardent leur propre licence, toutes permissives et
compatibles avec l'AGPL-3.0 : MIT (pion, go-qrcode, go-webview2), BSD
(bibliothèque standard Go, golang.org/x, goupnp, uuid, anet), ISC
(coder/websocket, go-winloader) et Apache-2.0 (ebml-go). Leurs textes complets
sont intégrés à l'exécutable et consultables dans l'app (Paramètres → *Licences
open source*). `cloudflared` n'est pas distribué avec GhostCam : il est
téléchargé depuis Cloudflare à l'exécution, sous licence Apache-2.0.

# Symbolic protocol models

These ProVerif models check the v2 pairing flow and its encrypted response framing. They use Dolev–Yao symbolic cryptography. They do not prove the Go or Android implementations correct, and they do not prove the security of X25519, ML-KEM-768, HKDF-SHA256, SHA-256, AES-GCM, or their composition.

## Run the models

Run:

```sh
sh tools/protocolverify/check.sh
```

Unless `PROVERIF` is set, the runner downloads ProVerif 2.05 from the official INRIA site, checks SHA-256 `4871f53c32ab4a04669a060c4886ba5d9080496963fb980a9a62d2c429ceabc4`, extracts and builds it in the user's cache, and runs the models. The source archive is [proverif2.05.tar.gz](https://proverif.inria.fr/proverif2.05.tar.gz). The build needs `ocaml-nox`, a C compiler, `tar`, `curl`, `sha256sum`, and `awk`. No compiler or ProVerif binary is checked into the repository.

Set `PROVERIF_REPORT_DIR=/path/to/report` to retain each verifier transcript.
To use a local installation, set `PROVERIF=/path/to/proverif`. The verified version for this change is 2.05.

The runner checks every expected positive result and requires each negative control to produce its expected attack trace. A parser error, failed query, or missing trace makes the command fail.

## What the models cover

`pairing.pv` models one v2 pairing exchange. The host publishes a bundle signed by the P-256 identity key. The signed fields include the suite, epoch, P-256 key, X25519 key, and ML-KEM key; the host ID is separate metadata. The client checks the signature, requires the v2 suite, and compares a suite-bound fingerprint with the fingerprint delivered over a private out-of-band channel before sending the code. It seals the code with an idealized AEAD key derived from both the DH and KEM secrets plus the bundle hash, host ID, and pair-material hash. The host checks v2 and the suite before opening the code. The token response uses a separate symbolic key derived with a transcript that also includes the device ID, and it uses that transcript as AEAD associated data.

The queries check that the attacker cannot learn the one-time code or device token, that a completed pairing matches a host completion, and that any host acceptance matches the client's code-sending event on the same host, suite, fingerprint, ephemeral DH public key, and KEM ciphertext. That last query checks the pair-init binding against network changes to the key material. Separate reachability queries confirm that the honest client completion, host acceptance, and response completion events can occur, so the secrecy and correspondence results do not pass only because the modeled protocol is stuck.

`hybrid-dh-leak.pv` and `hybrid-kem-leak.pv` model the key combiner with one hybrid input compromised at a time. The token remains secret when the attacker learns either the host's DH private value or the ML-KEM private value. These are combiner checks, not host-compromise proofs for the full service.

`response-terminator.pv` starts after key agreement and models encrypted response head, body, and terminal marker frames. Data frames bind the device ID, request ID, and frame index as AAD. The terminal frame uses the same values plus the `end` label. The server can either finish or abort after the body. The client emits completion only after it authenticates the marker. `broken-no-terminator.pv` models a client that accepts after the head and body; ProVerif finds a counterexample when the server aborts. The pairing response model uses a separate symbolic response key and binds its AEAD to the pair transcript, which includes the device ID. The model does not represent byte-stream EOF or trailing bytes, so it does not prove the Android parser's exact end-of-stream rule.

`broken-raw-code.pv` and `broken-unpinned-bundle.pv` are negative controls. The first exposes the pairing code as plaintext. The second lets a self-signed, attacker-controlled bundle pass without the independent fingerprint comparison. ProVerif must find the code disclosure in both models. The second models the first-pair substitution that motivated checking the desktop fingerprint before the `/pair` request.

The pairing model has one host process and one client process. It does not model concurrent requests, expiry, persistent replay state, rate limits, scope enforcement, device revocation, a previously stored host pin or epoch, HTTP parsing, TLS, or the human error of comparing the wrong fingerprint. The model's client and host accept only v2 and the exact hybrid suite, so changing the request to v1 cannot reach host acceptance. The model does not include saved-device v1 compatibility or the explicit v1 host configuration. Its private fingerprint channel assumes the user obtains the expected value from the intended desktop and confirms it before the client sends the code. That assumption is necessary: a signed bundle authenticates its own included key, not the intended host by itself.

The model treats the signed bundle, hash, KDF, DH, KEM, and AEAD as ideal symbolic operators. Its results establish only the stated reachability and correspondence properties for these abstract roles under those assumptions. They are not quantitative security claims, a cryptographic proof of the algorithms, or evidence that the implementation matches the model.

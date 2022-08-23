# Polymorphs-V1 Rarity Backend
## Summary
- This is the latest and most up-to-date version of the V1 rarity backend.
- It is still supported because by the time of this commit, relative small amount of V1s are burned into V1s(~17%) so V1 contract is fully usable
## Deployment
- The process runs in a docker container on GCE
  - Build the docker image
    - ```bash
      docker build --platform linux/amd64 --tag polymorphs-rarity-v2 .
      ```
- Notes:
  - This backend only queries the contract ~ 15 seconds for specific events, and updates the Mongo DB accordingly
  - The mongo collections themselves are queried using the following gcloud function: `https://github.com/UniverseXYZ/Polymorph-Rarity-Cloud`
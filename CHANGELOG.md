# Changelog

- All notable changes to this project will be documented in this file.
- The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).
- We try to follow [semantic versioning](https://semver.org/).
- All changes have a **git tag** available, are build and published to GitHub packages as a docker image.

## Unreleased

## v1.1.0
- First of all, even though this is a **major refactor**, the clientside API is still the same. **There should be no breaking changes!**
- The main goal of this release is to bring IntegreSQL's performance on par with our previous Node.js implementation. Specifially we wanted to eliminate any long-running mutex locks and make the codebase more maintainable and easier to extend in the future.
- ...

## v1.0.0
- Initial release May 2020

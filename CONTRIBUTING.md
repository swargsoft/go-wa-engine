# Contributing to WA Engine

Thank you for your interest in contributing to WA Engine! This guide will help you get started.

## Commit Message Convention

We follow [Conventional Commits](https://www.conventionalcommits.org/) for clear and structured commit messages.

### Format

```
<type>: <short description>

[optional body]

[optional footer]
```

### Types

- **feat:** A new feature

  ```
  feat: add multi-session support for up to 5 accounts
  feat: implement connection-aware polling
  ```

- **fix:** A bug fix

  ```
  fix: resolve QR code not appearing during pairing
  fix: correct DNS resolution in emulator
  ```

- **chore:** Maintenance or build-related tasks

  ```
  chore: update dependencies
  chore: clean up unused imports
  ```

- **docs:** Documentation changes

  ```
  docs: add API reference for session management
  docs: update README with Docker build instructions
  ```

- **refactor:** Code changes that neither fix bugs nor add features

  ```
  refactor: simplify event queue implementation
  refactor: extract storage logic into separate module
  ```

- **test:** Adding or updating tests

  ```
  test: add unit tests for message sending
  test: update integration tests for pairing flow
  ```

- **perf:** Performance improvements

  ```
  perf: optimize polling loop battery usage
  ```

- **style:** Code style changes (formatting, whitespace)
  ```
  style: format code with gofmt
  ```

### Examples

```bash
# Good commit messages
git commit -m "feat: add platform identification as Chrome Ubuntu"
git commit -m "fix: handle whatsmeow API breaking changes"
git commit -m "docs: update README with GitHub Actions workflow"
git commit -m "chore: add .gitignore for build artifacts"

# Bad commit messages (avoid these)
git commit -m "updated code"
git commit -m "fixes"
git commit -m "changes"
```

## Development Workflow

### 1. Fork and Clone

```bash
# Fork the repository on GitHub, then:
git clone https://github.com/YOUR_USERNAME/wa-engine.git
cd wa-engine
```

### 2. Create a Branch

```bash
# Use descriptive branch names
git checkout -b feat/add-message-reactions
git checkout -b fix/memory-leak-in-polling
git checkout -b docs/improve-api-reference
```

### 3. Make Changes

- Write clear, documented code
- Follow Go best practices
- Add tests for new features
- Update documentation

### 4. Test Your Changes

```bash
# Run Go tests
go test ./...

# Build AAR locally with Docker (optional)
cd bindings/android
docker build -f Dockerfile -t wa-engine-builder ../..
```

### 5. Commit Your Changes

```bash
# Stage your changes
git add .

# Commit with conventional commit message
git commit -m "feat: add support for message reactions"

# If you need to provide more details:
git commit -m "feat: add support for message reactions

- Implement SendReaction method
- Add reaction event handling
- Update documentation with examples
"
```

### 6. Push and Create Pull Request

```bash
# Push to your fork
git push origin feat/add-message-reactions

# Then create a Pull Request on GitHub
```

## Building the AAR

The AAR is automatically built via GitHub Actions when you push a tag:

```bash
# Create and push a version tag
git tag v1.0.1
git push origin v1.0.1
```

The GitHub Action will:

1. Build the AAR in Docker
2. Create a GitHub Release
3. Attach the AAR file to the release

## Code Style

### Go Code

- Use `gofmt` for formatting
- Follow [Effective Go](https://go.dev/doc/effective_go) guidelines
- Add comments for exported functions and types
- Keep functions small and focused

### Kotlin Code (Android bindings)

- Use 2-space indentation
- Follow Android Kotlin style guide
- Document public APIs with KDoc

### TypeScript/JavaScript (React Native bindings)

- Use 2-space indentation
- Use TypeScript types consistently
- Document with JSDoc comments

## Pull Request Guidelines

### Before Submitting

- [ ] Code follows project style guidelines
- [ ] Tests pass locally
- [ ] Documentation is updated
- [ ] Commit messages follow conventional commits
- [ ] Branch is up to date with main

### PR Description Template

```markdown
## Description

Brief description of what this PR does

## Type of Change

- [ ] Bug fix (fix:)
- [ ] New feature (feat:)
- [ ] Documentation update (docs:)
- [ ] Code refactoring (refactor:)
- [ ] Performance improvement (perf:)

## Testing

How was this tested?

## Related Issues

Closes #123
```

## Versioning

We use [Semantic Versioning](https://semver.org/):

- **MAJOR** (v2.0.0): Breaking changes
- **MINOR** (v1.1.0): New features (backward compatible)
- **PATCH** (v1.0.1): Bug fixes (backward compatible)

## Getting Help

- 📖 Read the [README.md](./README.md)
- 🐛 Open an [Issue](https://github.com/YOUR_ORG/wa-engine/issues)
- 💬 Start a [Discussion](https://github.com/YOUR_ORG/wa-engine/discussions)

## Code of Conduct

- Be respectful and inclusive
- Provide constructive feedback
- Focus on what is best for the community
- Show empathy towards others

Thank you for contributing! 🎉

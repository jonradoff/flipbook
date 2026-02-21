.PHONY: build run dev clean check-deps setup set-password deploy

build:
	go build -o bin/flipbook .

run: check-deps build
	./bin/flipbook

check-deps:
	@which soffice > /dev/null 2>&1 || which /Applications/LibreOffice.app/Contents/MacOS/soffice > /dev/null 2>&1 || (echo "ERROR: LibreOffice not found." && exit 1)
	@which pdftoppm > /dev/null 2>&1 || (echo "ERROR: pdftoppm not found. Install poppler." && exit 1)
	@echo "All dependencies found."

setup:
	mkdir -p web/static/js
	curl -L -o web/static/js/page-flip.browser.js \
		https://unpkg.com/page-flip@2.0.7/dist/js/page-flip.browser.js
	@echo "Frontend dependencies downloaded."

set-password: build
	./bin/flipbook set-password

deploy:
	fly deploy

clean:
	rm -rf bin/

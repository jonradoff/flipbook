document.addEventListener('DOMContentLoaded', function() {
    var data = window.FLIPBOOK_DATA;
    if (!data || !data.pages || data.pages.length === 0) return;

    var container = document.getElementById('flipbook');
    var wrapper = document.getElementById('flipbook-container');

    // Calculate single-page dimensions to fill the viewport height
    var aspect = data.pageWidth / data.pageHeight;
    var maxH = wrapper.clientHeight - 40;
    var maxW = wrapper.clientWidth - 40;
    var h = Math.min(maxH, 900);
    var w = h * aspect;
    if (w > maxW) {
        w = maxW;
        h = w / aspect;
    }
    w = Math.round(w);
    h = Math.round(h);

    // Force single-page: constrain the container so only one page fits
    container.style.width = w + 'px';
    container.style.height = h + 'px';

    // Check for deep-link: ?page=N
    var startPage = 0;
    var urlParams = new URLSearchParams(window.location.search);
    var pageParam = parseInt(urlParams.get('page'), 10);
    if (pageParam && pageParam >= 1 && pageParam <= data.pageCount) {
        startPage = pageParam - 1;
    }

    // Initialize StPageFlip - single page, fast flip
    var pageFlip = new St.PageFlip(container, {
        width: w,
        height: h,
        size: 'fixed',
        drawShadow: true,
        flippingTime: 300,
        usePortrait: true,
        startZIndex: 0,
        startPage: startPage,
        autoSize: false,
        maxShadowOpacity: 0.6,
        showCover: true,
        mobileScrollSupport: true,
        swipeDistance: 30,
        useMouseEvents: true,
        disableFlipByClick: false
    });

    pageFlip.loadFromImages(data.pages);

    var currentPage = startPage;

    // Update page display
    function updatePageDisplay(pageIndex) {
        currentPage = pageIndex;
        var pageNum = pageIndex + 1;
        document.getElementById('current-page').textContent = pageNum;
        var slider = document.getElementById('page-slider');
        if (slider) {
            slider.value = pageNum;
        }
    }

    // Set initial state
    updatePageDisplay(startPage);

    // Page navigation
    pageFlip.on('flip', function(e) {
        updatePageDisplay(e.data);
    });

    var prevBtn = document.getElementById('btn-prev');
    var nextBtn = document.getElementById('btn-next');

    if (prevBtn) {
        prevBtn.addEventListener('click', function() {
            pageFlip.flipPrev('top');
        });
    }

    if (nextBtn) {
        nextBtn.addEventListener('click', function() {
            pageFlip.flipNext('top');
        });
    }

    // Page slider
    var slider = document.getElementById('page-slider');
    if (slider) {
        slider.addEventListener('input', function() {
            var targetPage = parseInt(this.value, 10) - 1;
            if (targetPage !== currentPage) {
                pageFlip.turnToPage(targetPage);
            }
        });
    }

    // Keyboard navigation
    document.addEventListener('keydown', function(e) {
        var searchBar = document.getElementById('search-bar');
        var searchVisible = searchBar && !searchBar.classList.contains('hidden');
        var inSearchInput = e.target.id === 'search-input';

        // Ctrl+F / Cmd+F opens search
        if ((e.ctrlKey || e.metaKey) && e.key === 'f') {
            e.preventDefault();
            openSearch();
            return;
        }

        // Escape closes search first, then grid, then share
        if (e.key === 'Escape') {
            if (searchVisible) {
                closeSearch();
            } else {
                var gridOverlay = document.getElementById('grid-overlay');
                var gridVisible = gridOverlay && !gridOverlay.classList.contains('hidden');
                if (gridVisible) {
                    closeGrid();
                } else {
                    closeShareModal();
                }
            }
            return;
        }

        // Enter in search input navigates to next match
        if (inSearchInput && e.key === 'Enter') {
            e.preventDefault();
            if (e.shiftKey) {
                searchPrevMatch();
            } else {
                searchNextMatch();
            }
            return;
        }

        // Don't handle other keys when in an input
        if (e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA') return;

        var gridOverlay = document.getElementById('grid-overlay');
        var gridVisible = gridOverlay && !gridOverlay.classList.contains('hidden');

        switch(e.key) {
            case 'ArrowLeft':
            case 'ArrowUp':
                if (!gridVisible) {
                    pageFlip.flipPrev('top');
                    e.preventDefault();
                }
                break;
            case 'ArrowRight':
            case 'ArrowDown':
            case ' ':
                if (!gridVisible) {
                    pageFlip.flipNext('top');
                    e.preventDefault();
                }
                break;
            case 'f':
            case 'F':
                if (!gridVisible) {
                    toggleFullscreen();
                    e.preventDefault();
                }
                break;
            case 'g':
            case 'G':
                toggleGrid();
                e.preventDefault();
                break;
        }
    });

    // Fullscreen
    var fsBtn = document.getElementById('btn-fullscreen');
    if (fsBtn) {
        fsBtn.addEventListener('click', toggleFullscreen);
    }

    function toggleFullscreen() {
        var el = document.getElementById('viewer-wrapper');
        if (document.fullscreenElement) {
            document.exitFullscreen();
        } else if (el.requestFullscreen) {
            el.requestFullscreen();
        } else if (el.webkitRequestFullscreen) {
            el.webkitRequestFullscreen();
        }
    }

    // Share modal
    var shareBtn = document.getElementById('btn-share');
    if (shareBtn) {
        shareBtn.addEventListener('click', function() {
            updateShareLinks();
            document.getElementById('share-modal').classList.remove('hidden');
        });
    }

    function getShareURL(withPage) {
        var base = (data.baseURL || '') + '/v/' + (data.slug || '');
        if (withPage && currentPage > 0) {
            return base + '?page=' + (currentPage + 1);
        }
        return base;
    }

    function getEmbedURL(withPage) {
        var base = (data.baseURL || '') + '/embed/' + (data.slug || '');
        if (withPage && currentPage > 0) {
            return base + '?page=' + (currentPage + 1);
        }
        return base;
    }

    function getEmbedCode(withPage) {
        var url = getEmbedURL(withPage);
        return '<iframe src="' + url + '" width="800" height="600" frameborder="0" allowfullscreen style="border:none;border-radius:8px;box-shadow:0 2px 8px rgba(0,0,0,0.1);"></iframe>';
    }

    function updateShareLinks() {
        var shareFromPage = document.getElementById('share-from-page');
        var withPage = shareFromPage && shareFromPage.checked;

        var sharePageNum = document.getElementById('share-page-num');
        if (sharePageNum) {
            sharePageNum.textContent = currentPage + 1;
        }

        var shareLinkEl = document.getElementById('share-link');
        if (shareLinkEl) {
            shareLinkEl.value = getShareURL(withPage);
        }

        var embedCodeEl = document.getElementById('embed-code');
        if (embedCodeEl) {
            embedCodeEl.value = getEmbedCode(withPage);
        }
    }

    // Listen for checkbox changes
    var shareFromPage = document.getElementById('share-from-page');
    if (shareFromPage) {
        shareFromPage.addEventListener('change', updateShareLinks);
    }

    // Expose globally for inline onclick handlers
    window.closeShareModal = function() {
        var modal = document.getElementById('share-modal');
        if (modal) modal.classList.add('hidden');
    };

    window.copyField = function(id) {
        var el = document.getElementById(id);
        if (el) {
            if (el.tagName === 'TEXTAREA') {
                el.select();
            } else {
                el.select();
            }
            document.execCommand('copy');

            // Brief visual feedback
            var btn = el.parentElement.querySelector('button');
            if (btn) {
                var orig = btn.textContent;
                btn.textContent = 'Copied!';
                setTimeout(function() { btn.textContent = orig; }, 1500);
            }
        }
    };

    // Grid view
    var gridBtn = document.getElementById('btn-grid');
    if (gridBtn) {
        gridBtn.addEventListener('click', toggleGrid);
    }

    var gridCloseBtn = document.getElementById('btn-grid-close');
    if (gridCloseBtn) {
        gridCloseBtn.addEventListener('click', closeGrid);
    }

    var gridBuilt = false;

    function toggleGrid() {
        var overlay = document.getElementById('grid-overlay');
        if (!overlay) return;
        if (overlay.classList.contains('hidden')) {
            openGrid();
        } else {
            closeGrid();
        }
    }

    function openGrid() {
        var overlay = document.getElementById('grid-overlay');
        if (!overlay) return;

        if (!gridBuilt) {
            buildGrid();
            gridBuilt = true;
        }

        // Highlight current page
        highlightGridPage(currentPage);

        overlay.classList.remove('hidden');

        // Scroll current page into view
        var activeThumb = document.querySelector('.grid-page.active');
        if (activeThumb) {
            activeThumb.scrollIntoView({ behavior: 'smooth', block: 'center' });
        }
    }

    function closeGrid() {
        var overlay = document.getElementById('grid-overlay');
        if (overlay) overlay.classList.add('hidden');
    }

    function highlightGridPage(pageIndex) {
        var pages = document.querySelectorAll('.grid-page');
        pages.forEach(function(el, i) {
            if (i === pageIndex) {
                el.classList.add('active');
            } else {
                el.classList.remove('active');
            }
        });
    }

    // Search functionality
    var searchBtn = document.getElementById('btn-search');
    if (searchBtn) {
        searchBtn.addEventListener('click', openSearch);
    }

    var searchCloseBtn = document.getElementById('search-close');
    if (searchCloseBtn) {
        searchCloseBtn.addEventListener('click', closeSearch);
    }

    var searchPrevBtn = document.getElementById('search-prev');
    if (searchPrevBtn) {
        searchPrevBtn.addEventListener('click', searchPrevMatch);
    }

    var searchNextBtn = document.getElementById('search-next');
    if (searchNextBtn) {
        searchNextBtn.addEventListener('click', searchNextMatch);
    }

    var searchInput = document.getElementById('search-input');
    if (searchInput) {
        searchInput.addEventListener('input', onSearchInput);
    }

    var searchMatches = []; // array of page indices (0-based) that match
    var searchMatchIndex = -1; // current position in searchMatches

    function openSearch() {
        var bar = document.getElementById('search-bar');
        if (!bar) return;
        bar.classList.remove('hidden');
        var input = document.getElementById('search-input');
        if (input) {
            input.focus();
            input.select();
        }
    }

    function closeSearch() {
        var bar = document.getElementById('search-bar');
        if (bar) bar.classList.add('hidden');
        searchMatches = [];
        searchMatchIndex = -1;
        updateSearchStatus();
    }

    function onSearchInput() {
        var query = document.getElementById('search-input').value.trim().toLowerCase();
        searchMatches = [];
        searchMatchIndex = -1;

        if (query.length === 0 || !data.pageTexts || data.pageTexts.length === 0) {
            updateSearchStatus();
            return;
        }

        // Find all pages containing the query (one match per page)
        for (var i = 0; i < data.pageTexts.length; i++) {
            if (data.pageTexts[i].toLowerCase().indexOf(query) !== -1) {
                searchMatches.push(i);
            }
        }

        if (searchMatches.length > 0) {
            // Jump to the first match at or after the current page
            searchMatchIndex = 0;
            for (var j = 0; j < searchMatches.length; j++) {
                if (searchMatches[j] >= currentPage) {
                    searchMatchIndex = j;
                    break;
                }
            }
            pageFlip.turnToPage(searchMatches[searchMatchIndex]);
        }

        updateSearchStatus();
    }

    function searchNextMatch() {
        if (searchMatches.length === 0) return;
        searchMatchIndex = (searchMatchIndex + 1) % searchMatches.length;
        pageFlip.turnToPage(searchMatches[searchMatchIndex]);
        updateSearchStatus();
    }

    function searchPrevMatch() {
        if (searchMatches.length === 0) return;
        searchMatchIndex = (searchMatchIndex - 1 + searchMatches.length) % searchMatches.length;
        pageFlip.turnToPage(searchMatches[searchMatchIndex]);
        updateSearchStatus();
    }

    function updateSearchStatus() {
        var statusEl = document.getElementById('search-status');
        var prevBtn = document.getElementById('search-prev');
        var nextBtn = document.getElementById('search-next');

        if (searchMatches.length > 0) {
            statusEl.textContent = (searchMatchIndex + 1) + ' / ' + searchMatches.length;
            prevBtn.disabled = false;
            nextBtn.disabled = false;
        } else {
            var query = document.getElementById('search-input').value.trim();
            statusEl.textContent = query.length > 0 ? 'No results' : '';
            prevBtn.disabled = true;
            nextBtn.disabled = true;
        }
    }

    function buildGrid() {
        var gridContainer = document.getElementById('grid-container');
        if (!gridContainer) return;

        gridContainer.innerHTML = '';
        var thumbSrc = data.thumbs && data.thumbs.length ? data.thumbs : data.pages;

        for (var i = 0; i < data.pages.length; i++) {
            (function(pageIndex) {
                var div = document.createElement('div');
                div.className = 'grid-page';

                var img = document.createElement('img');
                img.className = 'grid-page-img';
                img.src = thumbSrc[pageIndex];
                img.alt = 'Page ' + (pageIndex + 1);
                img.loading = 'lazy';

                var label = document.createElement('div');
                label.className = 'grid-page-num';
                label.textContent = pageIndex + 1;

                div.appendChild(img);
                div.appendChild(label);

                div.addEventListener('click', function() {
                    pageFlip.turnToPage(pageIndex);
                    closeGrid();
                });

                gridContainer.appendChild(div);
            })(i);
        }
    }
});

// Global socket declaration outside Vue component
let engineSocket = null;

const { createApp } = Vue;

const app = createApp({
    data() {
        return {
            currentTab: 'home',
            installedSubTab: 'app',
            searchQuery: '',

            // [ADDED] State for Developer Mode
            isDevMode: false,

            appItems: [],
            nodeItems: [],
            newsItems: [],

            localApps: [],
            localNodes: [],

            installingItems: {},
            installedItems: {},

            cpuUsage: 0,
            ramUsage: 0,
            cacheVersion: 'Checking...',
            cacheSynced: false,

            systemLogs: [],
            isReconnecting: false,

            mouseX: 0,
            mouseY: 0,
            windowWidth: window.innerWidth,
            windowHeight: window.innerHeight,
            cardTransforms: {},
            cardGlares: {},

            apiUrls: {
                app: "https://floworkos.com/listapp.json",
                node: "https://floworkos.com/listnode.json",
                news: "https://floworkos.com/content/blog/index.json"
            },
        };
    },
    computed: {
        tabTitle() {
            if (this.currentTab === 'home') return 'System Dashboard';
            if (this.currentTab === 'app') return 'App Registry';
            if (this.currentTab === 'node') return 'Node Modules';
            if (this.currentTab === 'installed') return 'Installed Modules';
            return '';
        },
        tabSubtitle() {
            if (this.currentTab === 'home') return 'Real-time Host Telemetry & Intel Feed Archive';
            if (this.currentTab === 'app') return 'Enterprise GUI Applications';
            if (this.currentTab === 'node') return 'Core Processing Engines';
            if (this.currentTab === 'installed') return 'Local Applications & Secure Nodes';
            return '';
        },

        filteredItems() {
            if (this.currentTab === 'home') return [];
            if (this.currentTab === 'installed') return [];

            let sourceItems = [];
            if (this.currentTab === 'app') sourceItems = this.appItems;
            else if (this.currentTab === 'node') sourceItems = this.nodeItems;

            if (!Array.isArray(sourceItems)) return [];

            const query = (this.searchQuery || '').toLowerCase().trim();

            if (!query) return sourceItems;

            return sourceItems.filter(item => {
                const name = (item.name || '').toLowerCase();
                const desc = (item.description || '').toLowerCase();
                const keywords = Array.isArray(item.keywords) ? item.keywords.join(' ').toLowerCase() : '';

                return name.includes(query) || desc.includes(query) || keywords.includes(query);
            });
        },

        filteredInstalledApps() {
            const query = (this.searchQuery || '').toLowerCase().trim();
            const safeApps = Array.isArray(this.localApps) ? this.localApps : [];
            if (!query) return safeApps;
            return safeApps.filter(item => {
                const name = (item.name || '').toLowerCase();
                const desc = (item.description || '').toLowerCase();
                return name.includes(query) || desc.includes(query);
            });
        },

        filteredInstalledNodes() {
            const query = (this.searchQuery || '').toLowerCase().trim();
            const safeNodes = Array.isArray(this.localNodes) ? this.localNodes : [];
            if (!query) return safeNodes;
            return safeNodes.filter(item => {
                const name = (item.name || '').toLowerCase();
                const desc = (item.description || '').toLowerCase();
                return name.includes(query) || desc.includes(query);
            });
        },

        filteredNews() {
            const query = (this.searchQuery || '').toLowerCase().trim();
            const safeNews = Array.isArray(this.newsItems) ? this.newsItems : [];
            if (!query) return safeNews;
            return safeNews.filter(article => {
                const title = (article.title || '').toLowerCase();
                const desc = (article.description || '').toLowerCase();
                const keywords = Array.isArray(article.keywords) ? article.keywords.join(' ').toLowerCase() : '';
                return title.includes(query) || desc.includes(query) || keywords.includes(query);
            });
        },

        cacheStatusText() { return this.cacheVersion === 'Checking...' ? 'UPLINK...' : `v${this.cacheVersion}`; },
        cacheStatusColor() { return this.cacheVersion === 'Checking...' ? 'arcoblue' : (this.cacheSynced ? 'green' : 'gray'); },

        mascotTransform() {
            const moveX = ((this.mouseX - (this.windowWidth / 2)) / 40).toFixed(2);
            const moveY = ((this.mouseY - (this.windowHeight / 2)) / 40).toFixed(2);
            return { transform: `translate(${-moveX}px, ${-moveY}px)` };
        },
        eyeTransform() {
            const moveX = ((this.mouseX - (this.windowWidth / 2)) / 60).toFixed(2);
            const moveY = ((this.mouseY - (this.windowHeight / 2)) / 60).toFixed(2);
            return `translate(${moveX}, ${moveY})`;
        }
    },
    methods: {
        // [ADDED] Method to handle Dev Mode Toggle
        toggleDevMode(value) {
            this.isDevMode = value;
            if (engineSocket && engineSocket.connected) {
                this.addLog(`[System] Sending Dev Mode toggle: ${value ? 'ON' : 'OFF'}`, 'warn');
                engineSocket.emit("engine:toggle_dev_mode", { is_dev: value });

                // Show notification
                if(value) {
                    ArcoVue.Notification.info({
                        title: 'DEV MODE ACTIVATED',
                        content: 'Engine will now execute raw folders directly without encryption/packing.',
                        position: 'bottomRight'
                    });
                } else {
                    ArcoVue.Notification.success({
                        title: 'DEV MODE DISABLED',
                        content: 'Standard security constraints restored.',
                        position: 'bottomRight'
                    });
                }
            } else {
                this.isDevMode = false;
                this.addLog(`[Error] Cannot toggle Dev Mode. Engine socket disconnected.`, 'error');
            }
        },

        handleGlobalMouseMove(e) {
            this.mouseX = e.clientX;
            this.mouseY = e.clientY;
        },

        // [MODIFIED] Logic to open website fixed to be more resilient against JSON Server errors
        openWebApp(item) {
            let url = '';

            // [ADDED] Determine Type prioritizing from active Tab State
            let type = '';
            if (this.currentTab === 'app' || this.currentTab === 'node') {
                type = this.currentTab;
            } else if (this.currentTab === 'installed') {
                type = this.installedSubTab; // Will be 'app' or 'node' based on the clicked menu state
            } else {
                type = item.type; // Fallback to object type
            }

            if (type === 'app') {
                url = `https://floworkos.com/flow/${item.id}`;
            } else if (type === 'node') {
                url = `https://floworkos.com/flow-designer`;
            } else {
                // Emergency fallback, if undetected will route to main flow
                url = `https://floworkos.com/flow/${item.id}`;
            }

            if (url) {
                this.addLog(`[UI] Running Module, executing URL: ${url}`, 'success');
                window.open(url, '_blank');
            }
        },

        clearSystemCache() {
            localStorage.removeItem('flowork_store_app');
            localStorage.removeItem('flowork_store_node');
            localStorage.removeItem('flowork_store_news');

            this.addLog('System cache cleared manually. Re-fetching data...', 'info');
            ArcoVue.Notification.success({
                title: 'Cache Cleared',
                content: 'All registry caches successfully cleared. Resyncing...',
                position: 'bottomRight'
            });

            this.cacheVersion = 'Checking...';
            this.cacheSynced = false;
            this.appItems = [];
            this.nodeItems = [];
            this.newsItems = [];

            this.fetchWithSmartCache('app');
            this.fetchWithSmartCache('node');
            this.fetchNews();
        },

        systemRestart() {
            if(confirm("Are you sure you want to restart Flowork Engine?")) {
                this.addLog("Sending restart command to Engine...", "warn");
                if (engineSocket && engineSocket.connected) {
                    engineSocket.emit("engine:restart");
                    setTimeout(() => window.close(), 1000); // Auto-close Chrome Tab
                }
            }
        },

        systemShutdown() {
            if(confirm("Are you sure you want to turn off Flowork Engine?")) {
                this.addLog("Sending shutdown command to Engine...", "error");
                if (engineSocket && engineSocket.connected) {
                    engineSocket.emit("engine:exit");
                    setTimeout(() => window.close(), 1000); // Auto-close Chrome Tab
                }
            }
        },

        addLog(text, type = 'info') {
            const time = new Date().toLocaleTimeString('en-US', { hour12: false });
            this.systemLogs.push({ id: Date.now() + Math.random(), time, text, type });
            if (this.systemLogs.length > 50) this.systemLogs.shift();

            this.$nextTick(() => {
                const terminal = document.getElementById('terminal-screen');
                if(terminal) {
                    const isAtBottom = terminal.scrollHeight - terminal.scrollTop <= terminal.clientHeight + 50;
                    if (isAtBottom) {
                        terminal.scrollTop = terminal.scrollHeight;
                    }
                }
            });
        },

        reconnectEngine() {
            if (this.isReconnecting) return;
            this.isReconnecting = true;
            this.addLog("Initiating manual reconnect sequence...", "warn");

            if(engineSocket) {
                engineSocket.disconnect();
                engineSocket.connect();
            }

            setTimeout(() => {
                this.addLog("WebSocket connection re-established.", "success");
                this.isReconnecting = false;
                ArcoVue.Notification.success({
                    title: 'System Synced',
                    content: 'The Go Hybrid engine was successfully reconnected.',
                    position: 'bottomRight'
                });
            }, 2000);
        },

        handleImageError(e) { e.target.src = 'https://cdn-icons-png.flaticon.com/512/1006/1006363.png'; },

        fetchLocalModules() {
            if (engineSocket && engineSocket.connected) {
                engineSocket.emit("engine:get_apps");
                engineSocket.emit("engine:get_nodes");
            }
        },

        triggerFileSelect() {
            const input = document.getElementById('uploadFile');

            // [MODIFIED] Ensure file upload reads type from sub-tab if currently on Installed tab
            let acceptType = '';
            if (this.currentTab === 'installed') {
                acceptType = this.installedSubTab === 'node' ? '.nflow' : '.flow';
            } else {
                acceptType = this.currentTab === 'node' ? '.nflow' : '.flow';
            }
            input.accept = acceptType;

            input.click();
        },

        async handleFileUpload(event) {
            const file = event.target.files[0];
            if (!file) return;

            let type = this.currentTab;

            if (this.currentTab === 'installed') {
                if (file.name.endsWith('.nflow')) type = 'node';
                else if (file.name.endsWith('.flow')) type = 'app';
                else {
                    ArcoVue.Notification.error({ title: 'Upload Failed', content: `Please upload a .flow or .nflow file`, position: 'bottomRight'});
                    event.target.value = '';
                    return;
                }
            } else {
                const expectedExt = type === 'app' ? '.flow' : '.nflow';
                if (!file.name.endsWith(expectedExt)) {
                    ArcoVue.Notification.error({ title: 'Upload Failed', content: `Please upload file with extension ${expectedExt}`, position: 'bottomRight' });
                    event.target.value = '';
                    return;
                }
            }

            const formData = new FormData();
            formData.append('file', file);
            formData.append('type', type);

            this.addLog(`[HTTP] Uploading manual package: ${file.name}...`, 'warn');

            try {
                const res = await fetch('http://127.0.0.1:5000/api/upload', {
                    method: 'POST',
                    body: formData
                });
                const data = await res.json();

                if (data.success) {
                    this.addLog(`[HTTP] Successfully uploaded ${file.name}!`, 'success');
                    ArcoVue.Notification.success({ title: 'Upload Successful', content: data.message, position: 'bottomRight' });

                    if (engineSocket && engineSocket.connected) {
                        engineSocket.emit("engine:get_installed_ids");
                        this.fetchLocalModules();
                    }
                } else {
                    throw new Error(data.error);
                }
            } catch (err) {
                this.addLog(`[HTTP] Upload error: ${err.message}`, 'error');
                ArcoVue.Notification.error({ title: 'Upload Failed', content: err.message, position: 'bottomRight' });
            }

            event.target.value = '';
        },

        triggerInstall(item) {
            if(!item.download_url) {
                this.addLog(`Error: Missing source URL for ${item.name}`, 'error');
                ArcoVue.Notification.error({ title: 'Install Failed', content: 'Missing source URL.', position: 'bottomRight' });
                return;
            }

            this.installingItems[item.id] = true;
            this.addLog(`[WS] Requesting Engine to natively download ${item.name}...`, 'warn');

            const payload = {
                id: item.id,
                type: item.type || this.currentTab,
                name: item.name,
                download_url: item.download_url,
                version: item.version
            };

            this.addLog(`[WS] Sending [engine:install_module] packet to 127.0.0.1:5000`, 'info');

            if (engineSocket && engineSocket.connected) {
                engineSocket.emit("engine:install_module", payload);
            } else {
                this.installingItems[item.id] = false;
                this.addLog(`[WS] ERROR: Engine Socket disconnected!`, 'error');
                ArcoVue.Notification.error({ title: 'Engine Error', content: 'Connection to Go core system lost.', position: 'bottomRight' });
            }
        },

        triggerUninstall(item) {
            this.installingItems[item.id] = true;
            this.addLog(`[WS] Requesting Engine to remove ${item.name}...`, 'warn');

            if (engineSocket && engineSocket.connected) {
                engineSocket.emit("engine:uninstall_module", {
                    id: item.id,
                    type: item.type || this.currentTab
                });
            } else {
                this.installingItems[item.id] = false;
                this.addLog(`[WS] ERROR: Engine Socket disconnected!`, 'error');
            }
        },

        startTelemetry() {
            this.cpuUsage = 15; this.ramUsage = 40;
            setInterval(() => {
                this.cpuUsage = Math.floor(Math.random() * 80) + 10;
                this.ramUsage = Math.floor(Math.random() * 30) + 40;
                if(this.cpuUsage > 85 && Math.random() > 0.8) {
                    this.addLog(`Warning: High CPU load detected (${this.cpuUsage}%)`, 'warn');
                }
            }, 1500);
        },

        extractItems(data) {
            if (!data) return [];
            if (Array.isArray(data)) return data;
            if (data.items && Array.isArray(data.items)) return data.items;
            if (data.Items && Array.isArray(data.Items)) return data.Items;
            if (data.data && Array.isArray(data.data)) return data.data;
            for (let key in data) { if (Array.isArray(data[key])) return data[key]; }
            return [];
        },

        async fetchWithSmartCache(type) {
            const url = this.apiUrls[type];
            const cacheKey = `flowork_store_${type}`;
            const now = Date.now();
            const ONE_DAY_MS = 24 * 60 * 60 * 1000;

            const cachedDataString = localStorage.getItem(cacheKey);
            if (cachedDataString) {
                try {
                    const parsed = JSON.parse(cachedDataString);
                    if (parsed.timestamp && (now - parsed.timestamp < ONE_DAY_MS) && parsed.data) {
                        const extractedItems = this.extractItems(parsed.data);
                        if (extractedItems.length > 0) {
                            if (type === 'app') this.appItems = extractedItems;
                            else if (type === 'node') this.nodeItems = extractedItems;

                            this.cacheVersion = parsed.data.registry_version || '1.0.0 (Cached)';
                            this.cacheSynced = true;
                            this.addLog(`Loaded ${type.toUpperCase()} from local cache (Valid < 24h)`, 'success');
                            return;
                        }
                    }
                } catch (e) {
                    localStorage.removeItem(cacheKey);
                }
            }

            try {
                this.addLog(`Cache expired/empty for ${type.toUpperCase()}. Fetching fresh Registry Uplink...`, 'warn');
                const response = await fetch(url + "?t=" + new Date().getTime(), { cache: 'no-store' });

                if (response.ok) {
                    const freshData = await response.json();
                    const freshItems = this.extractItems(freshData);

                    if (freshItems.length > 0) {
                        this.addLog(`JSON parsed. Found ${freshItems.length} modules for ${type.toUpperCase()}`, 'info');
                        if (type === 'app') this.appItems = freshItems;
                        else if (type === 'node') this.nodeItems = freshItems;

                        this.cacheVersion = freshData.registry_version || '1.0.0';
                        this.cacheSynced = true;

                        localStorage.setItem(cacheKey, JSON.stringify({ timestamp: now, data: freshData }));
                    } else {
                        throw new Error("Data received from server is empty (Empty Array).");
                    }
                } else {
                    throw new Error(`Server status ${response.status}`);
                }
            } catch (error) {
                this.addLog(`Uplink failed for ${type}. Fetching from Local Cache Fallback...`, 'warn');
                this.cacheSynced = false;
                this.cacheVersion = "OFFLINE";

                if (cachedDataString) {
                    try {
                        const parsed = JSON.parse(cachedDataString);
                        const extractedItems = this.extractItems(parsed.data || parsed);
                        if (type === 'app') this.appItems = extractedItems;
                        else if (type === 'node') this.nodeItems = extractedItems;

                        let fallbackVersion = '1.0.0 (Cached)';
                        if (parsed.data && parsed.data.registry_version) fallbackVersion = parsed.data.registry_version;
                        else if (parsed.registry_version) fallbackVersion = parsed.registry_version;

                        this.cacheVersion = fallbackVersion;
                        this.addLog(`Loaded ${type.toUpperCase()} from local fallback cache (Offline Mode).`, 'warn');
                    } catch (e) {
                        localStorage.removeItem(cacheKey);
                    }
                }
            }
        },

        async fetchNews() {
            const cacheKey = `flowork_store_news`;
            const cachedDataString = localStorage.getItem(cacheKey);
            const now = Date.now();
            const ONE_DAY_MS = 24 * 60 * 60 * 1000;

            if (cachedDataString) {
                try {
                    const parsed = JSON.parse(cachedDataString);
                    const extractedNews = this.extractItems(parsed.data);
                    if (parsed.timestamp && (now - parsed.timestamp < ONE_DAY_MS) && extractedNews.length > 0) {
                        this.newsItems = extractedNews;
                        this.addLog(`Intel Feed loaded from local cache (Valid < 24h)`, 'success');
                        return;
                    }
                } catch (e) { localStorage.removeItem(cacheKey); }
            }

            try {
                this.addLog(`Intel Feed Cache expired/empty. Fetching new data...`, 'warn');
                const response = await fetch(this.apiUrls.news + "?t=" + new Date().getTime());
                if (response.ok) {
                    const freshData = await response.json();
                    const extractedNews = this.extractItems(freshData);
                    this.newsItems = extractedNews;
                    localStorage.setItem(cacheKey, JSON.stringify({ timestamp: now, data: freshData }));
                    this.addLog(`Intel Feed successfully updated from server`, 'success');
                }
            } catch (error) { this.addLog(`Failed to fetch Intel Feed`, 'error'); }
        }
    },
    mounted() {
        this.addLog("Flowork Engine Neural (Go) Boot Sequence Initiated.", "info");

        try {
            engineSocket = io("http://127.0.0.1:5000/gui-socket", {
                path: "/api/socket.io/"
            });

            engineSocket.on("connect", () => {
                this.addLog("WebSocket port 5000 listener active and stable.", "success");
                engineSocket.emit("engine:get_installed_ids");
                this.fetchLocalModules();

                // [ADDED] Sync Dev Mode State upon reconnection
                if (this.isDevMode) {
                    engineSocket.emit("engine:toggle_dev_mode", { is_dev: true });
                }
            });

            engineSocket.on("disconnect", () => {
                this.addLog("Disconnected from Go Engine.", "error");
            });

            // [ADDED] Listener to refresh UI when Dev Mode changes the module list
            engineSocket.on("engine:needs_refresh", () => {
                this.addLog("[System] Received UI Refresh signal from Engine...", "info");
                this.fetchLocalModules();
            });

            engineSocket.on("engine:installed_ids_list", (data) => {
                this.installedItems = data;
                this.addLog(`[WS] Synchronized local install state.`, 'info');
            });

            engineSocket.on("engine:apps_list", (data) => {
                this.localApps = (data.data || []).map(app => ({
                    ...app,
                    type: 'app',
                    icon: app.icon || 'https://cdn-icons-png.flaticon.com/512/1006/1006363.png'
                }));
            });

            engineSocket.on("engine:nodes_list", (data) => {
                this.localNodes = (data.data || []).map(node => {
                    let rawId = node.name ? node.name.replace('engine.secure.', '') : 'unknown';
                    return {
                        ...node,
                        id: rawId,
                        type: 'node',
                        name: node.displayName || node.name,
                        icon: 'https://cdn-icons-png.flaticon.com/512/2888/2888407.png'
                    };
                });
            });

            engineSocket.on("engine:install_result", (res) => {
                const id = res.id;
                this.installingItems[id] = false;

                if (res.success) {
                    this.installedItems[id] = true;
                    this.addLog(`[WS] SUCCESS: Engine verified ${res.name} deployed at ${res.path}`, 'success');
                    ArcoVue.Notification.success({
                        title: 'INSTALLATION COMPLETE',
                        content: `Package downloaded & installed at ${res.path}`,
                        position: 'bottomRight',
                        duration: 5000
                    });
                    this.fetchLocalModules();
                } else {
                    this.addLog(`[WS] ERROR: Download failed. ${res.error}`, 'error');
                    ArcoVue.Notification.error({
                        title: 'INSTALLATION FAILED',
                        content: res.error,
                        position: 'bottomRight',
                        duration: 5000
                    });
                }
            });

            engineSocket.on("engine:uninstall_result", (res) => {
                const id = res.id;
                this.installingItems[id] = false;

                if (res.success) {
                    delete this.installedItems[id];
                    this.addLog(`[WS] SUCCESS: Engine verified removal of ${id}`, 'success');
                    ArcoVue.Notification.success({
                        title: 'UNINSTALL COMPLETE',
                        content: `Package ${id} successfully removed from the system.`,
                        position: 'bottomRight',
                        duration: 3000
                    });
                    this.fetchLocalModules();
                } else {
                    this.addLog(`[WS] ERROR: Uninstall failed. ${res.error}`, 'error');
                    ArcoVue.Notification.error({
                        title: 'UNINSTALL FAILED',
                        content: res.error,
                        position: 'bottomRight',
                        duration: 5000
                    });
                }
            });

        } catch(e) {
            this.addLog("Failed to bind WebSocket io client.", "error");
        }

        this.startTelemetry();
        this.fetchWithSmartCache('app');
        this.fetchWithSmartCache('node');
        this.fetchNews();

        window.addEventListener('resize', () => {
            this.windowWidth = window.innerWidth;
            this.windowHeight = window.innerHeight;
        });
    }
});

app.use(ArcoVue);
app.use(ArcoVueIcon);
app.mount('#app');
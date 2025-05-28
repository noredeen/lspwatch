// WebSocket connection
const ws = new WebSocket('ws://' + window.location.host + '/ws');
const messagesDiv = document.getElementById('messages');

function createCollapsibleContent(text, maxLines = 30) {
    const lines = text.split('\n');
    const isLong = lines.length > maxLines;
    
    const content = document.createElement('pre');
    if (isLong) {
        // Show first maxLines lines
        content.textContent = lines.slice(0, maxLines).join('\n');
        content.classList.add('collapsed');
        
        // Create expand button
        const expandBtn = document.createElement('button');
        expandBtn.className = 'expand-btn';
        expandBtn.textContent = 'Show more';
        expandBtn.onclick = function() {
            if (content.classList.contains('collapsed')) {
                content.textContent = text;
                content.classList.remove('collapsed');
                expandBtn.textContent = 'Show less';
            } else {
                content.textContent = lines.slice(0, maxLines).join('\n');
                content.classList.add('collapsed');
                expandBtn.textContent = 'Show more';
            }
        };
        
        return { content, expandBtn };
    } else {
        content.textContent = text;
        return { content };
    }
}

ws.onmessage = function(event) {
    const msg = JSON.parse(event.data);
    const messageDiv = document.createElement('div');
    messageDiv.className = 'message ' + msg.direction;
    
    const header = document.createElement('div');
    header.className = 'message-header';
    
    // Create left side with direction and timestamp
    const leftSide = document.createElement('span');
    const date = new Date(msg.timestamp);
    const prettyDate = date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
    leftSide.textContent = msg.direction.toUpperCase() + ' - ' + prettyDate;
    
    // Create right side with duration if available
    const rightSide = document.createElement('span');
    if (msg.duration !== undefined && msg.duration >= 0) {
        rightSide.textContent = `Took: ${msg.duration}ms`;
    }
    
    header.appendChild(leftSide);
    header.appendChild(rightSide);
    
    const { content, expandBtn } = createCollapsibleContent(JSON.stringify(JSON.parse(msg.content), null, 2));
    
    messageDiv.appendChild(header);
    messageDiv.appendChild(content);
    if (expandBtn) {
        messageDiv.appendChild(expandBtn);
    }
    messagesDiv.appendChild(messageDiv);
    messagesDiv.scrollTop = messagesDiv.scrollHeight;
};
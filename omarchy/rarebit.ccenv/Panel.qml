// Panel.qml — the profile picker. Reads `ccenv list --json` (registry only: no
// credentials, no Claude calls) and launches Desktop through `ccenv desktop`,
// so the user data directory and CLAUDE_CONFIG_DIR always come from ccenv.
// Opening a profile that is already running focuses its window.
import QtQuick
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui
import "Ccenv.js" as Ccenv

Panel {
  id: root
  moduleName: "rarebit.ccenv"
  ipcTarget: "rarebit.ccenv"

  property var anchorItem: null
  property var hostWidget: null

  property var profiles: []
  property string error: ""
  readonly property bool anyRunning: {
    for (var i = 0; i < profiles.length; i++) if (profiles[i].desktop_running) return true
    return false
  }

  function refresh() { if (!listProc.running) listProc.running = true }
  function openFromHotkey() { root.controller.show(); refresh() }
  function toggle() { if (root.opened) root.close(); else openFromHotkey() }

  function launch(name, url) {
    var cmd = ["uwsm-app", "--", Ccenv.BIN, "desktop", "--profile", name]
    if (url) cmd.push("--", url)
    Quickshell.execDetached(cmd)
    root.close()
    refreshSoon.restart()
  }

  Process {
    id: listProc
    command: [Ccenv.BIN, "list", "--json"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        try {
          root.profiles = JSON.parse(String(text || "{}")).profiles || []
          root.error = ""
        } catch (e) {
          root.profiles = []
          root.error = "ccenv list --json returned unreadable output"
        }
      }
    }
    onExited: function(code) { if (code !== 0) root.error = "ccenv list --json failed (exit " + code + ")" }
  }

  // Running state changes outside the panel: poll while open, and settle once
  // shortly after a launch so the bar glyph catches up.
  Timer { interval: 5000; repeat: true; running: root.opened; onTriggered: root.refresh() }
  Timer { id: refreshSoon; interval: 4000; onTriggered: root.refresh() }
  Timer { interval: 60000; repeat: true; running: true; onTriggered: root.refresh() }
  Component.onCompleted: refresh()

  KeyboardPanel {
    id: panel
    anchorItem: root.anchorItem
    owner: root.barIdentity
    bar: root.bar
    open: root.opened
    centerOnBar: false
    contentWidth: 380
    contentHeight: Math.min(560, body.implicitHeight + 32)

    Flickable {
      anchors.fill: parent
      contentWidth: width
      contentHeight: body.implicitHeight
      clip: true
      boundsBehavior: Flickable.StopAtBounds
      interactive: contentHeight > height

      Column {
        id: body
        width: parent.width
        spacing: 10

        Text { text: "Claude Desktop profiles"; font.bold: true; font.pixelSize: 16; color: Color.foreground }

        Text {
          visible: root.error !== ""
          width: parent.width; wrapMode: Text.WordWrap
          text: root.error; color: Color.foreground; font.pixelSize: 12
        }

        Repeater {
          model: root.profiles
          delegate: Item {
            width: body.width
            height: row.implicitHeight + 12

            Rectangle {
              anchors.fill: parent
              radius: 6
              color: Color.foreground
              opacity: rowArea.containsMouse ? 0.08 : 0
            }
            MouseArea {
              id: rowArea
              anchors.fill: parent
              hoverEnabled: true
              cursorShape: Qt.PointingHandCursor
              onClicked: root.launch(modelData.name, "")
            }

            Row {
              id: row
              anchors.verticalCenter: parent.verticalCenter
              x: 8
              width: parent.width - 16
              spacing: 10

              Text {
                anchors.verticalCenter: parent.verticalCenter
                text: "●"; font.pixelSize: 12
                color: modelData.desktop_running ? Color.foreground : Color.muted
                opacity: modelData.desktop_running ? 1 : 0.35
              }
              Column {
                width: parent.width - 120
                Row {
                  spacing: 6
                  Text { text: modelData.name; font.bold: true; font.pixelSize: 13; color: Color.foreground }
                  Text { visible: modelData["default"] === true; text: "default"; font.pixelSize: 10; color: Color.muted; anchors.baseline: parent.children[0].baseline }
                }
                Text { text: modelData.email || "no pinned account"; font.pixelSize: 11; color: Color.muted; elide: Text.ElideRight; width: parent.width }
              }
              // Jump straight into a new chat or Claude Code session for this profile.
              Text {
                anchors.verticalCenter: parent.verticalCenter
                text: "chat"; font.pixelSize: 11; color: chatArea.containsMouse ? Color.foreground : Color.muted
                MouseArea { id: chatArea; anchors.fill: parent; anchors.margins: -4; hoverEnabled: true; cursorShape: Qt.PointingHandCursor
                  onClicked: root.launch(modelData.name, "claude://claude.ai/new?surface=chat&source=desktop_action") }
              }
              Text {
                anchors.verticalCenter: parent.verticalCenter
                text: "code"; font.pixelSize: 11; color: codeArea.containsMouse ? Color.foreground : Color.muted
                MouseArea { id: codeArea; anchors.fill: parent; anchors.margins: -4; hoverEnabled: true; cursorShape: Qt.PointingHandCursor
                  onClicked: root.launch(modelData.name, "claude://code/new?source=desktop_action") }
              }
            }
          }
        }

        Text {
          visible: root.profiles.length === 0 && root.error === ""
          width: parent.width; wrapMode: Text.WordWrap; color: Color.muted; font.pixelSize: 12
          text: "No ccenv profiles registered. Run `ccenv discover` to find Claude config folders, then `ccenv add`."
        }

        Rectangle { width: parent.width; height: 1; color: Color.muted; opacity: 0.25 }

        Text {
          width: parent.width; wrapMode: Text.WordWrap; color: Color.muted; font.pixelSize: 11
          text: "Each profile keeps its own Desktop login. Sign in once per profile; claude:// sign-in links go to the profile you launched last. Middle-click the bar glyph to refresh."
        }
      }
    }
  }
}

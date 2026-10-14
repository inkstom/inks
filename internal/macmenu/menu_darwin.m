#import <Cocoa/Cocoa.h>
#import <objc/runtime.h>
#include "_cgo_export.h"

@interface InksMenuTarget : NSObject
@end

@implementation InksMenuTarget
- (void)fire:(NSMenuItem *)item {
	NSEvent *ev = [NSApp currentEvent];
	// Triggered by its shortcut: hand the key to the window so the editor's own
	// key routing (chords, text fields, user bindings) decides what it means.
	if (ev.type == NSEventTypeKeyDown && item.keyEquivalent.length > 0 &&
	    [[ev.charactersIgnoringModifiers lowercaseString] isEqualToString:[item.keyEquivalent lowercaseString]]) {
		NSWindow *w = [NSApp keyWindow];
		NSResponder *r = w.firstResponder;
		if (r != nil && r != w && [r respondsToSelector:@selector(keyDown:)]) {
			[r keyDown:ev];
			return;
		}
	}
	inksMenuFire((int)item.tag);
}
@end

static InksMenuTarget *target;

static NSMenu *buildMenu(NSString *title, NSArray *items) {
	NSMenu *menu = [[NSMenu alloc] initWithTitle:title];
	menu.autoenablesItems = NO;
	for (NSDictionary *it in items) {
		if ([it[@"sep"] boolValue]) {
			[menu addItem:[NSMenuItem separatorItem]];
			continue;
		}
		NSString *t = it[@"title"] ?: @"";
		NSString *key = it[@"key"] ?: @"";
		NSMenuItem *mi = [[NSMenuItem alloc] initWithTitle:t action:nil keyEquivalent:key];
		mi.keyEquivalentModifierMask = [it[@"mods"] unsignedIntegerValue];
		NSString *sel = it[@"sel"];
		if (sel.length > 0) {
			mi.action = NSSelectorFromString(sel);
		} else if (it[@"id"]) {
			mi.action = @selector(fire:);
			mi.target = target;
			mi.tag = [it[@"id"] integerValue];
		}
		mi.enabled = ![it[@"disabled"] boolValue];
		NSArray *sub = it[@"sub"];
		NSString *role = it[@"role"];
		if (sub || role) {
			NSMenu *sm = buildMenu(t, sub ?: @[]);
			mi.submenu = sm;
			if ([role isEqualToString:@"services"]) {
				NSApp.servicesMenu = sm;
			} else if ([role isEqualToString:@"window"]) {
				NSApp.windowsMenu = sm;
			} else if ([role isEqualToString:@"help"]) {
				NSApp.helpMenu = sm;
			}
		}
		[menu addItem:mi];
	}
	return menu;
}

void inks_set_menu(const char *json, int len) {
	NSData *data = [NSData dataWithBytes:json length:len];
	dispatch_async(dispatch_get_main_queue(), ^{
		if (target == nil) {
			target = [InksMenuTarget new];
		}
		NSArray *menus = [NSJSONSerialization JSONObjectWithData:data options:0 error:nil];
		if (![menus isKindOfClass:[NSArray class]]) {
			return;
		}
		NSMenu *bar = [[NSMenu alloc] initWithTitle:@""];
		for (NSDictionary *m in menus) {
			NSMenuItem *top = [[NSMenuItem alloc] initWithTitle:m[@"title"] ?: @"" action:nil keyEquivalent:@""];
			NSMenu *sm = buildMenu(m[@"title"] ?: @"", m[@"sub"] ?: @[]);
			top.submenu = sm;
			NSString *role = m[@"role"];
			if ([role isEqualToString:@"window"]) {
				NSApp.windowsMenu = sm;
			} else if ([role isEqualToString:@"help"]) {
				NSApp.helpMenu = sm;
			}
			[bar addItem:top];
		}
		NSApp.mainMenu = bar;
	});
}

static NSApplicationTerminateReply inksAppShouldTerminate(id self, SEL _cmd, NSApplication *app) {
	return inksShouldTerminate() ? NSTerminateNow : NSTerminateCancel;
}

void inks_install_terminate(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		id del = NSApp.delegate;
		if (del == nil) {
			return;
		}
		class_addMethod([del class], @selector(applicationShouldTerminate:),
		                (IMP)inksAppShouldTerminate, "Q@:@");
	});
}

static void dumpMenu(NSMutableString *out, NSMenu *menu, int depth) {
	for (NSMenuItem *mi in menu.itemArray) {
		if (mi.isSeparatorItem) {
			continue;
		}
		for (int i = 0; i < depth; i++) {
			[out appendString:@"  "];
		}
		[out appendString:mi.title];
		if (mi.keyEquivalent.length > 0) {
			NSEventModifierFlags m = mi.keyEquivalentModifierMask;
			[out appendFormat:@" [%@%@%@%@%@]", (m & NSEventModifierFlagControl) ? @"⌃" : @"", (m & NSEventModifierFlagOption) ? @"⌥" : @"",
			 (m & NSEventModifierFlagShift) ? @"⇧" : @"", (m & NSEventModifierFlagCommand) ? @"⌘" : @"", mi.keyEquivalent];
		}
		if (!mi.enabled) {
			[out appendString:@" (disabled)"];
		}
		[out appendString:@"\n"];
		if (mi.submenu) {
			dumpMenu(out, mi.submenu, depth + 1);
		}
	}
}

char *inks_dump_menu(void) {
	__block char *res = NULL;
	dispatch_sync(dispatch_get_main_queue(), ^{
		NSMutableString *out = [NSMutableString string];
		dumpMenu(out, NSApp.mainMenu, 0);
		res = strdup(out.UTF8String);
	});
	return res;
}

export namespace audio {
	
	export class Chunk {
	    id: string;
	    offset: number;
	    size: number;
	
	    static createFrom(source: any = {}) {
	        return new Chunk(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.offset = source["offset"];
	        this.size = source["size"];
	    }
	}
	export class WEM {
	    format: number;
	    channels: number;
	    sampleRate: number;
	    avgBytesPerSecond: number;
	    channelMask: number;
	    chunks: Chunk[];
	    samples: number;
	    modSignal: number;
	    setupOffset: number;
	    audioOffset: number;
	    uid: number;
	    blocksize0: number;
	    blocksize1: number;
	    modPackets: boolean;
	    dataOffset: number;
	    dataSize: number;
	    loopStart?: number;
	    loopEnd?: number;
	    hasLoop?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new WEM(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.format = source["format"];
	        this.channels = source["channels"];
	        this.sampleRate = source["sampleRate"];
	        this.avgBytesPerSecond = source["avgBytesPerSecond"];
	        this.channelMask = source["channelMask"];
	        this.chunks = this.convertValues(source["chunks"], Chunk);
	        this.samples = source["samples"];
	        this.modSignal = source["modSignal"];
	        this.setupOffset = source["setupOffset"];
	        this.audioOffset = source["audioOffset"];
	        this.uid = source["uid"];
	        this.blocksize0 = source["blocksize0"];
	        this.blocksize1 = source["blocksize1"];
	        this.modPackets = source["modPackets"];
	        this.dataOffset = source["dataOffset"];
	        this.dataSize = source["dataSize"];
	        this.loopStart = source["loopStart"];
	        this.loopEnd = source["loopEnd"];
	        this.hasLoop = source["hasLoop"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace catalog {
	
	export class KindCount {
	    kind: string;
	    label: string;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new KindCount(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.label = source["label"];
	        this.count = source["count"];
	    }
	}
	export class NodeRow {
	    guid: string;
	    type: number;
	    name?: string;
	    guess?: boolean;
	    note?: string;
	    size: number;
	    missing?: boolean;
	    kinds?: string[];
	    aux?: number;
	    in: number;
	    out: number;
	
	    static createFrom(source: any = {}) {
	        return new NodeRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.guid = source["guid"];
	        this.type = source["type"];
	        this.name = source["name"];
	        this.guess = source["guess"];
	        this.note = source["note"];
	        this.size = source["size"];
	        this.missing = source["missing"];
	        this.kinds = source["kinds"];
	        this.aux = source["aux"];
	        this.in = source["in"];
	        this.out = source["out"];
	    }
	}
	export class TypeCount {
	    type: number;
	    label: string;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new TypeCount(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.label = source["label"];
	        this.count = source["count"];
	    }
	}
	export class NeighborPage {
	    ready: boolean;
	    all: number;
	    total: number;
	    start: number;
	    byType: TypeCount[];
	    byKind: KindCount[];
	    rows: NodeRow[];
	
	    static createFrom(source: any = {}) {
	        return new NeighborPage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ready = source["ready"];
	        this.all = source["all"];
	        this.total = source["total"];
	        this.start = source["start"];
	        this.byType = this.convertValues(source["byType"], TypeCount);
	        this.byKind = this.convertValues(source["byKind"], KindCount);
	        this.rows = this.convertValues(source["rows"], NodeRow);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class NeighborQuery {
	    guid: string;
	    dir: string;
	    types: number[];
	    package: string;
	    kinds: string[];
	    search: string;
	    noMissing: boolean;
	    start: number;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new NeighborQuery(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.guid = source["guid"];
	        this.dir = source["dir"];
	        this.types = source["types"];
	        this.package = source["package"];
	        this.kinds = source["kinds"];
	        this.search = source["search"];
	        this.noMissing = source["noMissing"];
	        this.start = source["start"];
	        this.count = source["count"];
	    }
	}
	
	export class PackageRow {
	    guid: string;
	    index: number;
	    manifest: string;
	    records: number;
	    size: number;
	    name?: string;
	
	    static createFrom(source: any = {}) {
	        return new PackageRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.guid = source["guid"];
	        this.index = source["index"];
	        this.manifest = source["manifest"];
	        this.records = source["records"];
	        this.size = source["size"];
	        this.name = source["name"];
	    }
	}
	export class Row {
	    guid: string;
	    type: number;
	    size: number;
	    name?: string;
	    guess?: boolean;
	    note?: string;
	
	    static createFrom(source: any = {}) {
	        return new Row(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.guid = source["guid"];
	        this.type = source["type"];
	        this.size = source["size"];
	        this.name = source["name"];
	        this.guess = source["guess"];
	        this.note = source["note"];
	    }
	}
	export class Page {
	    total: number;
	    start: number;
	    rows: Row[];
	
	    static createFrom(source: any = {}) {
	        return new Page(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.total = source["total"];
	        this.start = source["start"];
	        this.rows = this.convertValues(source["rows"], Row);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Query {
	    type: number;
	    package: string;
	    search: string;
	    start: number;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new Query(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.package = source["package"];
	        this.search = source["search"];
	        this.start = source["start"];
	        this.count = source["count"];
	    }
	}
	
	
	export class TypeRow {
	    type: number;
	    label: string;
	    source: string;
	    count: number;
	    bytes: number;
	    stu: boolean;
	
	    static createFrom(source: any = {}) {
	        return new TypeRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.label = source["label"];
	        this.source = source["source"];
	        this.count = source["count"];
	        this.bytes = source["bytes"];
	        this.stu = source["stu"];
	    }
	}
	export class TypeStats {
	    type: number;
	    label: string;
	    source: string;
	    stu: boolean;
	    count: number;
	    bytes: number;
	    min: number;
	    median: number;
	    max: number;
	
	    static createFrom(source: any = {}) {
	        return new TypeStats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.label = source["label"];
	        this.source = source["source"];
	        this.stu = source["stu"];
	        this.count = source["count"];
	        this.bytes = source["bytes"];
	        this.min = source["min"];
	        this.median = source["median"];
	        this.max = source["max"];
	    }
	}

}

export namespace main {
	
	export class AskSubject {
	    kind: string;
	    label?: string;
	    guid?: string;
	    map?: string;
	    record?: number;
	    node?: number;
	    instance?: number;
	    path?: string;
	    mip?: number;
	    submesh?: number;
	    look?: string;
	
	    static createFrom(source: any = {}) {
	        return new AskSubject(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.label = source["label"];
	        this.guid = source["guid"];
	        this.map = source["map"];
	        this.record = source["record"];
	        this.node = source["node"];
	        this.instance = source["instance"];
	        this.path = source["path"];
	        this.mip = source["mip"];
	        this.submesh = source["submesh"];
	        this.look = source["look"];
	    }
	}
	export class AskMsg {
	    id: string;
	    role: string;
	    text: string;
	    subject?: AskSubject;
	    at: number;
	    state?: string;
	
	    static createFrom(source: any = {}) {
	        return new AskMsg(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.role = source["role"];
	        this.text = source["text"];
	        this.subject = this.convertValues(source["subject"], AskSubject);
	        this.at = source["at"];
	        this.state = source["state"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AskState {
	    listeners: number;
	    hub: number;
	    events: string;
	    messages: AskMsg[];
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new AskState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.listeners = source["listeners"];
	        this.hub = source["hub"];
	        this.events = source["events"];
	        this.messages = this.convertValues(source["messages"], AskMsg);
	        this.error = source["error"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class AudioPeaks {
	    channels: number[][];
	    seconds: number;
	    rate: number;
	    peak: number;
	    rms: number;
	
	    static createFrom(source: any = {}) {
	        return new AudioPeaks(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.channels = source["channels"];
	        this.seconds = source["seconds"];
	        this.rate = source["rate"];
	        this.peak = source["peak"];
	        this.rms = source["rms"];
	    }
	}
	export class NamedView {
	    label: string;
	    name: string;
	    id: string;
	    hashOk: boolean;
	    group?: NamedRow;
	    switches: NamedRow[];
	    same: NamedRow[];
	    banks: string[];
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new NamedView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.label = source["label"];
	        this.name = source["name"];
	        this.id = source["id"];
	        this.hashOk = source["hashOk"];
	        this.group = this.convertValues(source["group"], NamedRow);
	        this.switches = this.convertValues(source["switches"], NamedRow);
	        this.same = this.convertValues(source["same"], NamedRow);
	        this.banks = source["banks"];
	        this.error = source["error"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class NamedRow {
	    guid: string;
	    type: number;
	    name: string;
	    id: string;
	
	    static createFrom(source: any = {}) {
	        return new NamedRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.guid = source["guid"];
	        this.type = source["type"];
	        this.name = source["name"];
	        this.id = source["id"];
	    }
	}
	export class HircRow {
	    id: string;
	    type: number;
	    typeName: string;
	    size: number;
	    offset: number;
	    parent?: string;
	    detail?: string;
	    wem?: string;
	
	    static createFrom(source: any = {}) {
	        return new HircRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.type = source["type"];
	        this.typeName = source["typeName"];
	        this.size = source["size"];
	        this.offset = source["offset"];
	        this.parent = source["parent"];
	        this.detail = source["detail"];
	        this.wem = source["wem"];
	    }
	}
	export class BankView {
	    version: number;
	    id: string;
	    chunks: audio.Chunk[];
	    counts: Record<string, number>;
	    objects: HircRow[];
	    events: HircNode[];
	    sounds: string[];
	    groups: NamedRow[];
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new BankView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.id = source["id"];
	        this.chunks = this.convertValues(source["chunks"], audio.Chunk);
	        this.counts = source["counts"];
	        this.objects = this.convertValues(source["objects"], HircRow);
	        this.events = this.convertValues(source["events"], HircNode);
	        this.sounds = source["sounds"];
	        this.groups = this.convertValues(source["groups"], NamedRow);
	        this.error = source["error"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class HircNode {
	    id: string;
	    type: number;
	    typeName: string;
	    detail?: string;
	    wem?: string;
	    seconds?: number;
	    missing?: boolean;
	    children?: HircNode[];
	    more?: number;
	
	    static createFrom(source: any = {}) {
	        return new HircNode(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.type = source["type"];
	        this.typeName = source["typeName"];
	        this.detail = source["detail"];
	        this.wem = source["wem"];
	        this.seconds = source["seconds"];
	        this.missing = source["missing"];
	        this.children = this.convertValues(source["children"], HircNode);
	        this.more = source["more"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class WEMRow {
	    guid: string;
	    id: string;
	    stream: boolean;
	    seconds: number;
	    channels: number;
	    rate: number;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new WEMRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.guid = source["guid"];
	        this.id = source["id"];
	        this.stream = source["stream"];
	        this.seconds = source["seconds"];
	        this.channels = source["channels"];
	        this.rate = source["rate"];
	        this.error = source["error"];
	    }
	}
	export class SoundView {
	    eventPlay: string;
	    eventStop: string;
	    bank?: string;
	    bankId: string;
	    files: WEMRow[];
	    events: HircNode[];
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new SoundView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.eventPlay = source["eventPlay"];
	        this.eventStop = source["eventStop"];
	        this.bank = source["bank"];
	        this.bankId = source["bankId"];
	        this.files = this.convertValues(source["files"], WEMRow);
	        this.events = this.convertValues(source["events"], HircNode);
	        this.error = source["error"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class WEMView {
	    guid: string;
	    header?: audio.WEM;
	    seconds: number;
	    packets: number;
	    voice: boolean;
	    error?: string;
	    sounds: string[];
	    banks: string[];
	    users: string[];
	    id?: string;
	
	    static createFrom(source: any = {}) {
	        return new WEMView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.guid = source["guid"];
	        this.header = this.convertValues(source["header"], audio.WEM);
	        this.seconds = source["seconds"];
	        this.packets = source["packets"];
	        this.voice = source["voice"];
	        this.error = source["error"];
	        this.sounds = source["sounds"];
	        this.banks = source["banks"];
	        this.users = source["users"];
	        this.id = source["id"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AudioView {
	    kind: string;
	    wem?: WEMView;
	    sound?: SoundView;
	    bank?: BankView;
	    named?: NamedView;
	    ready: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AudioView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.wem = this.convertValues(source["wem"], WEMView);
	        this.sound = this.convertValues(source["sound"], SoundView);
	        this.bank = this.convertValues(source["bank"], BankView);
	        this.named = this.convertValues(source["named"], NamedView);
	        this.ready = source["ready"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class BoneView {
	    parent: number;
	    id: string;
	    pos: number[];
	
	    static createFrom(source: any = {}) {
	        return new BoneView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.parent = source["parent"];
	        this.id = source["id"];
	        this.pos = source["pos"];
	    }
	}
	export class DecoderStatus {
	    name: string;
	    status: string;
	    detail: string;
	    planned?: string;
	
	    static createFrom(source: any = {}) {
	        return new DecoderStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.status = source["status"];
	        this.detail = source["detail"];
	        this.planned = source["planned"];
	    }
	}
	export class ElementView {
	    semantic: string;
	    index: number;
	    format: string;
	    stream: number;
	    offset: number;
	
	    static createFrom(source: any = {}) {
	        return new ElementView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.semantic = source["semantic"];
	        this.index = source["index"];
	        this.format = source["format"];
	        this.stream = source["stream"];
	        this.offset = source["offset"];
	    }
	}
	export class HexPage {
	    offset: number;
	    size: number;
	    data: number[];
	
	    static createFrom(source: any = {}) {
	        return new HexPage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.offset = source["offset"];
	        this.size = source["size"];
	        this.data = source["data"];
	    }
	}
	
	
	export class PackageDetail {
	    records: number;
	    deps: number;
	    bundle?: string;
	    owner?: string;
	    manifest: string;
	
	    static createFrom(source: any = {}) {
	        return new PackageDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.records = source["records"];
	        this.deps = source["deps"];
	        this.bundle = source["bundle"];
	        this.owner = source["owner"];
	        this.manifest = source["manifest"];
	    }
	}
	export class InfoView {
	    stats: catalog.TypeStats;
	    decoder: DecoderStatus;
	    ready: boolean;
	    out: catalog.NeighborPage;
	    in: catalog.NeighborPage;
	    package?: PackageDetail;
	
	    static createFrom(source: any = {}) {
	        return new InfoView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.stats = this.convertValues(source["stats"], catalog.TypeStats);
	        this.decoder = this.convertValues(source["decoder"], DecoderStatus);
	        this.ready = source["ready"];
	        this.out = this.convertValues(source["out"], catalog.NeighborPage);
	        this.in = this.convertValues(source["in"], catalog.NeighborPage);
	        this.package = this.convertValues(source["package"], PackageDetail);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class LODView {
	    mask: number;
	    triangles: number;
	    submeshes: number;
	
	    static createFrom(source: any = {}) {
	        return new LODView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mask = source["mask"];
	        this.triangles = source["triangles"];
	        this.submeshes = source["submeshes"];
	    }
	}
	export class LookOption {
	    guid: string;
	    name?: string;
	    source: string;
	    resolved: number;
	
	    static createFrom(source: any = {}) {
	        return new LookOption(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.guid = source["guid"];
	        this.name = source["name"];
	        this.source = source["source"];
	        this.resolved = source["resolved"];
	    }
	}
	export class MapEnvironmentView {
	    sunDir: number[];
	    sunColor: number[];
	    sky: string;
	    ambient: string;
	    reflection: string;
	    lut: string;
	    skyModel: string;
	    skyLook: string;
	    floats: number[];
	
	    static createFrom(source: any = {}) {
	        return new MapEnvironmentView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sunDir = source["sunDir"];
	        this.sunColor = source["sunColor"];
	        this.sky = source["sky"];
	        this.ambient = source["ambient"];
	        this.reflection = source["reflection"];
	        this.lut = source["lut"];
	        this.skyModel = source["skyModel"];
	        this.skyLook = source["skyLook"];
	        this.floats = source["floats"];
	    }
	}
	export class ProbeView {
	    record: number;
	    pos: number[];
	    scale: number[];
	    rot: number[];
	    extents: number[];
	    hdr: string;
	    ldr: string;
	    floats: number[];
	    exposure: number;
	
	    static createFrom(source: any = {}) {
	        return new ProbeView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.record = source["record"];
	        this.pos = source["pos"];
	        this.scale = source["scale"];
	        this.rot = source["rot"];
	        this.extents = source["extents"];
	        this.hdr = source["hdr"];
	        this.ldr = source["ldr"];
	        this.floats = source["floats"];
	        this.exposure = source["exposure"];
	    }
	}
	export class MapProbesView {
	    probes: ProbeView[];
	    levels: number[];
	    mips: number[];
	
	    static createFrom(source: any = {}) {
	        return new MapProbesView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.probes = this.convertValues(source["probes"], ProbeView);
	        this.levels = source["levels"];
	        this.mips = source["mips"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class MapVisualBatch {
	    model: string;
	    look?: string;
	    instances: number[];
	    records: number[];
	    lod: number;
	    triangles: number;
	    colors: string[];
	    helpers: boolean[];
	    opacity: string[];
	    decals: boolean[];
	    materials: string[];
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new MapVisualBatch(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.model = source["model"];
	        this.look = source["look"];
	        this.instances = source["instances"];
	        this.records = source["records"];
	        this.lod = source["lod"];
	        this.triangles = source["triangles"];
	        this.colors = source["colors"];
	        this.helpers = source["helpers"];
	        this.opacity = source["opacity"];
	        this.decals = source["decals"];
	        this.materials = source["materials"];
	        this.error = source["error"];
	    }
	}
	export class MapVisualView {
	    batches: MapVisualBatch[];
	    instances: number;
	    triangles: number;
	    failed: number;
	
	    static createFrom(source: any = {}) {
	        return new MapVisualView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.batches = this.convertValues(source["batches"], MapVisualBatch);
	        this.instances = source["instances"];
	        this.triangles = source["triangles"];
	        this.failed = source["failed"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class MatBufferView {
	    hash: string;
	    size: number;
	    floats: number[];
	
	    static createFrom(source: any = {}) {
	        return new MatBufferView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hash = source["hash"];
	        this.size = source["size"];
	        this.floats = source["floats"];
	    }
	}
	export class MatParamView {
	    hash: string;
	    type: number;
	    values: number[];
	    use?: string;
	
	    static createFrom(source: any = {}) {
	        return new MatParamView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hash = source["hash"];
	        this.type = source["type"];
	        this.values = source["values"];
	        this.use = source["use"];
	    }
	}
	export class MatTextureView {
	    guid: string;
	    slot: string;
	    slotName?: string;
	    flags: number;
	    format?: string;
	    width: number;
	    height: number;
	    missing?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new MatTextureView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.guid = source["guid"];
	        this.slot = source["slot"];
	        this.slotName = source["slotName"];
	        this.flags = source["flags"];
	        this.format = source["format"];
	        this.width = source["width"];
	        this.height = source["height"];
	        this.missing = source["missing"];
	    }
	}
	export class MaterialView {
	    guid: string;
	    shader: string;
	    ref?: string;
	    color: number[];
	    helper: boolean;
	    flags68: number;
	    textures: MatTextureView[];
	    buffers: MatBufferView[];
	    params: MatParamView[];
	    preview: model.Preview;
	
	    static createFrom(source: any = {}) {
	        return new MaterialView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.guid = source["guid"];
	        this.shader = source["shader"];
	        this.ref = source["ref"];
	        this.color = source["color"];
	        this.helper = source["helper"];
	        this.flags68 = source["flags68"];
	        this.textures = this.convertValues(source["textures"], MatTextureView);
	        this.buffers = this.convertValues(source["buffers"], MatBufferView);
	        this.params = this.convertValues(source["params"], MatParamView);
	        this.preview = this.convertValues(source["preview"], model.Preview);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class McpInfo {
	    port: number;
	    ports: number[];
	    primary: boolean;
	    drive: boolean;
	    requests: number;
	    failed: number;
	    last?: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new McpInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.port = source["port"];
	        this.ports = source["ports"];
	        this.primary = source["primary"];
	        this.drive = source["drive"];
	        this.requests = source["requests"];
	        this.failed = source["failed"];
	        this.last = source["last"];
	        this.error = source["error"];
	    }
	}
	export class SkeletonView {
	    hash: string;
	    bones: BoneView[];
	
	    static createFrom(source: any = {}) {
	        return new SkeletonView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hash = source["hash"];
	        this.bones = this.convertValues(source["bones"], BoneView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SlotView {
	    key: string;
	    material?: string;
	    shader?: string;
	    color?: string;
	    opacity?: string;
	    decal?: boolean;
	    normal?: string;
	    textures: number;
	    submeshes: number;
	    helper?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SlotView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.material = source["material"];
	        this.shader = source["shader"];
	        this.color = source["color"];
	        this.opacity = source["opacity"];
	        this.decal = source["decal"];
	        this.normal = source["normal"];
	        this.textures = source["textures"];
	        this.submeshes = source["submeshes"];
	        this.helper = source["helper"];
	    }
	}
	export class VBView {
	    count: number;
	    hash: string;
	    strides: number[];
	    elements: ElementView[];
	
	    static createFrom(source: any = {}) {
	        return new VBView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.count = source["count"];
	        this.hash = source["hash"];
	        this.strides = source["strides"];
	        this.elements = this.convertValues(source["elements"], ElementView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ModelView {
	    guid: string;
	    size: number;
	    split: number[];
	    lods: LODView[];
	    vbs: VBView[];
	    ibs: number[];
	    submeshes: model.Submesh[];
	    look: string;
	    looks: LookOption[];
	    slots: SlotView[];
	    skeleton?: SkeletonView;
	    refs: string[];
	    opened: string;
	
	    static createFrom(source: any = {}) {
	        return new ModelView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.guid = source["guid"];
	        this.size = source["size"];
	        this.split = source["split"];
	        this.lods = this.convertValues(source["lods"], LODView);
	        this.vbs = this.convertValues(source["vbs"], VBView);
	        this.ibs = source["ibs"];
	        this.submeshes = this.convertValues(source["submeshes"], model.Submesh);
	        this.look = source["look"];
	        this.looks = this.convertValues(source["looks"], LookOption);
	        this.slots = this.convertValues(source["slots"], SlotView);
	        this.skeleton = this.convertValues(source["skeleton"], SkeletonView);
	        this.refs = source["refs"];
	        this.opened = source["opened"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class NodeBrief {
	    index: number;
	    kind: string;
	    short: string;
	    net: string;
	
	    static createFrom(source: any = {}) {
	        return new NodeBrief(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.kind = source["kind"];
	        this.short = source["short"];
	        this.net = source["net"];
	    }
	}
	
	
	export class RefDetail {
	    kinds: string[];
	    paths: string[];
	    aux?: number;
	
	    static createFrom(source: any = {}) {
	        return new RefDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kinds = source["kinds"];
	        this.paths = source["paths"];
	        this.aux = source["aux"];
	    }
	}
	export class ResourceInfo {
	    guid: string;
	    type: number;
	    typeLabel: string;
	    size: number;
	    ckey: string;
	    flags: number;
	    packages: string[];
	    name: names.Name;
	    kind: string;
	    magic: string;
	    text?: string;
	    refsIn: number;
	    refsOut: number;
	    pinned: boolean;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new ResourceInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.guid = source["guid"];
	        this.type = source["type"];
	        this.typeLabel = source["typeLabel"];
	        this.size = source["size"];
	        this.ckey = source["ckey"];
	        this.flags = source["flags"];
	        this.packages = source["packages"];
	        this.name = this.convertValues(source["name"], names.Name);
	        this.kind = source["kind"];
	        this.magic = source["magic"];
	        this.text = source["text"];
	        this.refsIn = source["refsIn"];
	        this.refsOut = source["refsOut"];
	        this.pinned = source["pinned"];
	        this.error = source["error"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	
	export class Status {
	    open: boolean;
	    opening: boolean;
	    install: string;
	    describe: string;
	    resources: number;
	    packages: number;
	    types: number;
	    openMs: number;
	    error?: string;
	    index: refindex.Progress;
	    repo: string;
	    notes: string[];
	
	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.open = source["open"];
	        this.opening = source["opening"];
	        this.install = source["install"];
	        this.describe = source["describe"];
	        this.resources = source["resources"];
	        this.packages = source["packages"];
	        this.types = source["types"];
	        this.openMs = source["openMs"];
	        this.error = source["error"];
	        this.index = this.convertValues(source["index"], refindex.Progress);
	        this.repo = source["repo"];
	        this.notes = source["notes"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class TexDisplay {
	    exposure: number;
	    auto: boolean;
	    zFromXY: boolean;
	
	    static createFrom(source: any = {}) {
	        return new TexDisplay(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.exposure = source["exposure"];
	        this.auto = source["auto"];
	        this.zFromXY = source["zFromXY"];
	    }
	}
	export class TexHeaderHex {
	    bytes: string;
	
	    static createFrom(source: any = {}) {
	        return new TexHeaderHex(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.bytes = source["bytes"];
	    }
	}
	export class TexMip {
	    width: number;
	    height: number;
	    bytes: number;
	    available: boolean;
	    source: string;
	
	    static createFrom(source: any = {}) {
	        return new TexMip(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.width = source["width"];
	        this.height = source["height"];
	        this.bytes = source["bytes"];
	        this.available = source["available"];
	        this.source = source["source"];
	    }
	}
	export class TexPayload {
	    guid: string;
	    present: boolean;
	    first: number;
	    count: number;
	    top: number;
	    size: number;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new TexPayload(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.guid = source["guid"];
	        this.present = source["present"];
	        this.first = source["first"];
	        this.count = source["count"];
	        this.top = source["top"];
	        this.size = source["size"];
	        this.error = source["error"];
	    }
	}
	export class TextureView {
	    guid: string;
	    payload: string;
	    format: string;
	    dxgi: number;
	    decoder: boolean;
	    hdr: boolean;
	    channels: number;
	    srgb: boolean;
	    width: number;
	    height: number;
	    surfaces: number;
	    cube: boolean;
	    flags: number;
	    flagNames: string[];
	    unk5: number;
	    unk10: string;
	    base?: string;
	    inline: boolean;
	    dataSize: number;
	    mips: TexMip[];
	    payloads: TexPayload[];
	    variants: string[];
	    error?: string;
	    header: TexHeaderHex;
	
	    static createFrom(source: any = {}) {
	        return new TextureView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.guid = source["guid"];
	        this.payload = source["payload"];
	        this.format = source["format"];
	        this.dxgi = source["dxgi"];
	        this.decoder = source["decoder"];
	        this.hdr = source["hdr"];
	        this.channels = source["channels"];
	        this.srgb = source["srgb"];
	        this.width = source["width"];
	        this.height = source["height"];
	        this.surfaces = source["surfaces"];
	        this.cube = source["cube"];
	        this.flags = source["flags"];
	        this.flagNames = source["flagNames"];
	        this.unk5 = source["unk5"];
	        this.unk10 = source["unk10"];
	        this.base = source["base"];
	        this.inline = source["inline"];
	        this.dataSize = source["dataSize"];
	        this.mips = this.convertValues(source["mips"], TexMip);
	        this.payloads = this.convertValues(source["payloads"], TexPayload);
	        this.variants = source["variants"];
	        this.error = source["error"];
	        this.header = this.convertValues(source["header"], TexHeaderHex);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class UIState {
	    install: string;
	    pinned: string[];
	    recent: string[];
	    panels: Record<string, number>;
	
	    static createFrom(source: any = {}) {
	        return new UIState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.install = source["install"];
	        this.pinned = source["pinned"];
	        this.recent = source["recent"];
	        this.panels = source["panels"];
	    }
	}
	
	

}

export namespace maps {
	
	export class Bounds {
	    min: number[];
	    max: number[];
	
	    static createFrom(source: any = {}) {
	        return new Bounds(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.min = source["min"];
	        this.max = source["max"];
	    }
	}
	export class Capsule {
	    p0: number[];
	    p1: number[];
	    radius: number;
	    mask: number;
	    passThrough: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Capsule(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.p0 = source["p0"];
	        this.p1 = source["p1"];
	        this.radius = source["radius"];
	        this.mask = source["mask"];
	        this.passThrough = source["passThrough"];
	    }
	}
	export class CollisionMesh {
	    tris: number;
	    verts: number;
	    bvh_nodes: number;
	    bvh_depth: number;
	    tri_offset: number;
	    vert_offset: number;
	    bvh_offset: number;
	
	    static createFrom(source: any = {}) {
	        return new CollisionMesh(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tris = source["tris"];
	        this.verts = source["verts"];
	        this.bvh_nodes = source["bvh_nodes"];
	        this.bvh_depth = source["bvh_depth"];
	        this.tri_offset = source["tri_offset"];
	        this.vert_offset = source["vert_offset"];
	        this.bvh_offset = source["bvh_offset"];
	    }
	}
	export class Collision {
	    spheres: number;
	    capsules: number;
	    hulls: number;
	    meshes: CollisionMesh[];
	
	    static createFrom(source: any = {}) {
	        return new Collision(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.spheres = source["spheres"];
	        this.capsules = source["capsules"];
	        this.hulls = source["hulls"];
	        this.meshes = this.convertValues(source["meshes"], CollisionMesh);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class Filter {
	    mask: number;
	    passThrough: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Filter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mask = source["mask"];
	        this.passThrough = source["passThrough"];
	    }
	}
	export class Hull {
	    mask: number;
	    passThrough: boolean;
	    center: number[];
	    verts: number[][];
	    faces: number[][];
	    planes: number[][];
	
	    static createFrom(source: any = {}) {
	        return new Hull(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mask = source["mask"];
	        this.passThrough = source["passThrough"];
	        this.center = source["center"];
	        this.verts = source["verts"];
	        this.faces = source["faces"];
	        this.planes = source["planes"];
	    }
	}
	export class Section {
	    type: number;
	    name: string;
	    offset: number;
	    count: number;
	    size: number;
	
	    static createFrom(source: any = {}) {
	        return new Section(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.name = source["name"];
	        this.offset = source["offset"];
	        this.count = source["count"];
	        this.size = source["size"];
	    }
	}
	export class Shape {
	    kind: string;
	    pos: number[];
	    rot: number[];
	    half: number[];
	
	    static createFrom(source: any = {}) {
	        return new Shape(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.pos = source["pos"];
	        this.rot = source["rot"];
	        this.half = source["half"];
	    }
	}
	export class Sphere {
	    center: number[];
	    radius: number;
	    mask: number;
	    passThrough: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Sphere(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.center = source["center"];
	        this.radius = source["radius"];
	        this.mask = source["mask"];
	        this.passThrough = source["passThrough"];
	    }
	}

}

export namespace mapview {
	
	export class Asset {
	    guid: string;
	    type: number;
	    count: number;
	    name?: string;
	    missing?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Asset(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.guid = source["guid"];
	        this.type = source["type"];
	        this.count = source["count"];
	        this.name = source["name"];
	        this.missing = source["missing"];
	    }
	}
	export class BlobView {
	    slot: number;
	    offset: string;
	    size: number;
	    types: string[];
	    json: string;
	
	    static createFrom(source: any = {}) {
	        return new BlobView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.slot = source["slot"];
	        this.offset = source["offset"];
	        this.size = source["size"];
	        this.types = source["types"];
	        this.json = source["json"];
	    }
	}
	export class FieldRow {
	    name: string;
	    at: string;
	    value: string;
	    guid?: string;
	    refName?: string;
	
	    static createFrom(source: any = {}) {
	        return new FieldRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.at = source["at"];
	        this.value = source["value"];
	        this.guid = source["guid"];
	        this.refName = source["refName"];
	    }
	}
	export class Detail {
	    i: number;
	    type: number;
	    placed: number;
	    flags: number;
	    guid?: string;
	    name?: string;
	    look?: string;
	    pos: number[];
	    yaw: number;
	    hasPos: boolean;
	    class: string;
	    group?: number;
	    overrides?: string[];
	    offset: number;
	    size: number;
	    uuid: string;
	    fields: FieldRow[];
	    instanceData: BlobView[];
	    shapes?: maps.Shape[];
	    instances: number;
	
	    static createFrom(source: any = {}) {
	        return new Detail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.i = source["i"];
	        this.type = source["type"];
	        this.placed = source["placed"];
	        this.flags = source["flags"];
	        this.guid = source["guid"];
	        this.name = source["name"];
	        this.look = source["look"];
	        this.pos = source["pos"];
	        this.yaw = source["yaw"];
	        this.hasPos = source["hasPos"];
	        this.class = source["class"];
	        this.group = source["group"];
	        this.overrides = source["overrides"];
	        this.offset = source["offset"];
	        this.size = source["size"];
	        this.uuid = source["uuid"];
	        this.fields = this.convertValues(source["fields"], FieldRow);
	        this.instanceData = this.convertValues(source["instanceData"], BlobView);
	        this.shapes = this.convertValues(source["shapes"], maps.Shape);
	        this.instances = source["instances"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class EnvRow {
	    label: string;
	    guid: string;
	    name?: string;
	
	    static createFrom(source: any = {}) {
	        return new EnvRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.label = source["label"];
	        this.guid = source["guid"];
	        this.name = source["name"];
	    }
	}
	
	export class Placement {
	    i: number;
	    type: number;
	    placed: number;
	    flags: number;
	    guid?: string;
	    name?: string;
	    look?: string;
	    pos: number[];
	    yaw: number;
	    hasPos: boolean;
	    class: string;
	    group?: number;
	    overrides?: string[];
	    offset: number;
	
	    static createFrom(source: any = {}) {
	        return new Placement(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.i = source["i"];
	        this.type = source["type"];
	        this.placed = source["placed"];
	        this.flags = source["flags"];
	        this.guid = source["guid"];
	        this.name = source["name"];
	        this.look = source["look"];
	        this.pos = source["pos"];
	        this.yaw = source["yaw"];
	        this.hasPos = source["hasPos"];
	        this.class = source["class"];
	        this.group = source["group"];
	        this.overrides = source["overrides"];
	        this.offset = source["offset"];
	    }
	}
	export class Point {
	    i: number;
	    x: number;
	    y: number;
	    z: number;
	
	    static createFrom(source: any = {}) {
	        return new Point(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.i = source["i"];
	        this.x = source["x"];
	        this.y = source["y"];
	        this.z = source["z"];
	    }
	}
	export class RayHit {
	    hit: boolean;
	    t: number;
	    pos: number[];
	    normal: number[];
	    tri: number;
	    mask: number;
	    flags: number;
	    material: number;
	
	    static createFrom(source: any = {}) {
	        return new RayHit(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hit = source["hit"];
	        this.t = source["t"];
	        this.pos = source["pos"];
	        this.normal = source["normal"];
	        this.tri = source["tri"];
	        this.mask = source["mask"];
	        this.flags = source["flags"];
	        this.material = source["material"];
	    }
	}
	export class Shapes {
	    spheres: maps.Sphere[];
	    capsules: maps.Capsule[];
	    hulls: maps.Hull[];
	    tris: number;
	    verts: number;
	    nodes: number;
	    min: number[];
	    max: number[];
	    filter: maps.Filter;
	
	    static createFrom(source: any = {}) {
	        return new Shapes(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.spheres = this.convertValues(source["spheres"], maps.Sphere);
	        this.capsules = this.convertValues(source["capsules"], maps.Capsule);
	        this.hulls = this.convertValues(source["hulls"], maps.Hull);
	        this.tris = source["tris"];
	        this.verts = source["verts"];
	        this.nodes = source["nodes"];
	        this.min = source["min"];
	        this.max = source["max"];
	        this.filter = this.convertValues(source["filter"], maps.Filter);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class View {
	    guid: string;
	    size: number;
	    placed: number;
	    sections: maps.Section[];
	    collision: maps.Collision;
	    env: EnvRow[];
	    sunDir: number[];
	    camera: number[];
	    bounds: maps.Bounds[];
	    navSize: number;
	    tacticalSize: number;
	    soundLinks: number;
	    placements: Placement[];
	    points: Point[];
	    assets: Asset[];
	    classes: Record<string, number>;
	
	    static createFrom(source: any = {}) {
	        return new View(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.guid = source["guid"];
	        this.size = source["size"];
	        this.placed = source["placed"];
	        this.sections = this.convertValues(source["sections"], maps.Section);
	        this.collision = this.convertValues(source["collision"], maps.Collision);
	        this.env = this.convertValues(source["env"], EnvRow);
	        this.sunDir = source["sunDir"];
	        this.camera = source["camera"];
	        this.bounds = this.convertValues(source["bounds"], maps.Bounds);
	        this.navSize = source["navSize"];
	        this.tacticalSize = source["tacticalSize"];
	        this.soundLinks = source["soundLinks"];
	        this.placements = this.convertValues(source["placements"], Placement);
	        this.points = this.convertValues(source["points"], Point);
	        this.assets = this.convertValues(source["assets"], Asset);
	        this.classes = source["classes"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace model {
	
	export class Family {
	    id: number;
	    name: string;
	    mode: string;
	    geometry: string;
	    conf: string;
	    note?: string;
	
	    static createFrom(source: any = {}) {
	        return new Family(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.mode = source["mode"];
	        this.geometry = source["geometry"];
	        this.conf = source["conf"];
	        this.note = source["note"];
	    }
	}
	export class PreviewParam {
	    hash: string;
	    use: string;
	    values: number[];
	    conf: string;
	
	    static createFrom(source: any = {}) {
	        return new PreviewParam(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hash = source["hash"];
	        this.use = source["use"];
	        this.values = source["values"];
	        this.conf = source["conf"];
	    }
	}
	export class PreviewInput {
	    input: string;
	    texture: string;
	    slot: string;
	    slotName?: string;
	    channel: string;
	    format?: string;
	    uvSet: number;
	    on: boolean;
	    intensity?: number;
	    conf: string;
	    note?: string;
	
	    static createFrom(source: any = {}) {
	        return new PreviewInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.input = source["input"];
	        this.texture = source["texture"];
	        this.slot = source["slot"];
	        this.slotName = source["slotName"];
	        this.channel = source["channel"];
	        this.format = source["format"];
	        this.uvSet = source["uvSet"];
	        this.on = source["on"];
	        this.intensity = source["intensity"];
	        this.conf = source["conf"];
	        this.note = source["note"];
	    }
	}
	export class Preview {
	    family: Family;
	    inputs: PreviewInput[];
	    params: PreviewParam[];
	    unmapped: PreviewInput[];
	    uvScale: number[];
	    uvOffset: number[];
	    notes: string[];
	
	    static createFrom(source: any = {}) {
	        return new Preview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.family = this.convertValues(source["family"], Family);
	        this.inputs = this.convertValues(source["inputs"], PreviewInput);
	        this.params = this.convertValues(source["params"], PreviewParam);
	        this.unmapped = this.convertValues(source["unmapped"], PreviewInput);
	        this.uvScale = source["uvScale"];
	        this.uvOffset = source["uvOffset"];
	        this.notes = source["notes"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class Submesh {
	    bounds: number[];
	    vertexStart: number;
	    indexStart: number;
	    indexCount: number;
	    vertexCount: number;
	    flags: number;
	    vb: number;
	    ib: number;
	    material: number;
	    lod: number;
	
	    static createFrom(source: any = {}) {
	        return new Submesh(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.bounds = source["bounds"];
	        this.vertexStart = source["vertexStart"];
	        this.indexStart = source["indexStart"];
	        this.indexCount = source["indexCount"];
	        this.vertexCount = source["vertexCount"];
	        this.flags = source["flags"];
	        this.vb = source["vb"];
	        this.ib = source["ib"];
	        this.material = source["material"];
	        this.lod = source["lod"];
	    }
	}

}

export namespace names {
	
	export class Name {
	    text: string;
	    source: string;
	    guess: boolean;
	    note?: string;
	
	    static createFrom(source: any = {}) {
	        return new Name(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.text = source["text"];
	        this.source = source["source"];
	        this.guess = source["guess"];
	        this.note = source["note"];
	    }
	}

}

export namespace refindex {
	
	export class KindInfo {
	    name: string;
	    label: string;
	    source: string;
	
	    static createFrom(source: any = {}) {
	        return new KindInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.label = source["label"];
	        this.source = source["source"];
	    }
	}
	export class Progress {
	    phase: string;
	    done: number;
	    total: number;
	    seconds: number;
	    refs: number;
	    named: number;
	    failed: number;
	    cached: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Progress(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.phase = source["phase"];
	        this.done = source["done"];
	        this.total = source["total"];
	        this.seconds = source["seconds"];
	        this.refs = source["refs"];
	        this.named = source["named"];
	        this.failed = source["failed"];
	        this.cached = source["cached"];
	    }
	}

}

export namespace view {
	
	export class EntityRef {
	    path: string;
	    guid: string;
	    name?: string;
	
	    static createFrom(source: any = {}) {
	        return new EntityRef(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.guid = source["guid"];
	        this.name = source["name"];
	    }
	}
	export class EntityGraph {
	    guid: string;
	    name?: string;
	    overrides: number;
	
	    static createFrom(source: any = {}) {
	        return new EntityGraph(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.guid = source["guid"];
	        this.name = source["name"];
	        this.overrides = source["overrides"];
	    }
	}
	export class Component {
	    key: string;
	    keyHash: number;
	    type: string;
	    instance: number;
	    graphs: EntityGraph[];
	    refs: EntityRef[];
	
	    static createFrom(source: any = {}) {
	        return new Component(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.keyHash = source["keyHash"];
	        this.type = source["type"];
	        this.instance = source["instance"];
	        this.graphs = this.convertValues(source["graphs"], EntityGraph);
	        this.refs = this.convertValues(source["refs"], EntityRef);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class InstanceRow {
	    index: number;
	    offset: number;
	    flags: number;
	    crc: string;
	    type: string;
	    typeHash: number;
	    named: boolean;
	    crcOk: boolean;
	    root: boolean;
	    errors: number;
	    summary?: string;
	
	    static createFrom(source: any = {}) {
	        return new InstanceRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.offset = source["offset"];
	        this.flags = source["flags"];
	        this.crc = source["crc"];
	        this.type = source["type"];
	        this.typeHash = source["typeHash"];
	        this.named = source["named"];
	        this.crcOk = source["crcOk"];
	        this.root = source["root"];
	        this.errors = source["errors"];
	        this.summary = source["summary"];
	    }
	}
	export class Document {
	    size: number;
	    root: number;
	    allCrcOk: boolean;
	    instances: InstanceRow[];
	
	    static createFrom(source: any = {}) {
	        return new Document(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.size = source["size"];
	        this.root = source["root"];
	        this.allCrcOk = source["allCrcOk"];
	        this.instances = this.convertValues(source["instances"], InstanceRow);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Entity {
	    components: Component[];
	    refs: EntityRef[];
	
	    static createFrom(source: any = {}) {
	        return new Entity(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.components = this.convertValues(source["components"], Component);
	        this.refs = this.convertValues(source["refs"], EntityRef);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class GraphLayout {
	    pos: number[][];
	    routes: number[][][];
	    layers: number;
	    crossings: number;
	    reversed: number;
	
	    static createFrom(source: any = {}) {
	        return new GraphLayout(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pos = source["pos"];
	        this.routes = source["routes"];
	        this.layers = source["layers"];
	        this.crossings = source["crossings"];
	        this.reversed = source["reversed"];
	    }
	}
	export class GraphContainer {
	    instance: number;
	    type: string;
	    title?: string;
	    x: number;
	    y: number;
	    nodes: number[];
	    containers: number[];
	
	    static createFrom(source: any = {}) {
	        return new GraphContainer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.instance = source["instance"];
	        this.type = source["type"];
	        this.title = source["title"];
	        this.x = source["x"];
	        this.y = source["y"];
	        this.nodes = source["nodes"];
	        this.containers = source["containers"];
	    }
	}
	export class GraphLink {
	    from: number;
	    to: number;
	    fromPlug: string;
	    toPlug: string;
	    instance: number;
	
	    static createFrom(source: any = {}) {
	        return new GraphLink(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.from = source["from"];
	        this.to = source["to"];
	        this.fromPlug = source["fromPlug"];
	        this.toPlug = source["toPlug"];
	        this.instance = source["instance"];
	    }
	}
	export class Param {
	    name: string;
	    type: string;
	    instance: number;
	    summary: string;
	    refs: EntityRef[];
	
	    static createFrom(source: any = {}) {
	        return new Param(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.type = source["type"];
	        this.instance = source["instance"];
	        this.summary = source["summary"];
	        this.refs = this.convertValues(source["refs"], EntityRef);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Plug {
	    name: string;
	    in: boolean;
	    instance: number;
	    type: string;
	    links: number;
	
	    static createFrom(source: any = {}) {
	        return new Plug(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.in = source["in"];
	        this.instance = source["instance"];
	        this.type = source["type"];
	        this.links = source["links"];
	    }
	}
	export class GraphNode {
	    index: number;
	    instance: number;
	    kind: string;
	    type: string;
	    typeHash: number;
	    named: boolean;
	    short: string;
	    display?: string;
	    comment?: string;
	    debug?: string;
	    x: number;
	    y: number;
	    uniqueId: number;
	    stateIndex: number;
	    remoteSync: number;
	    scope: number;
	    clientOnly: boolean;
	    serverOnly: boolean;
	    net: string;
	    netWhy: string;
	    netConflict?: string;
	    plugs: Plug[];
	    params: Param[];
	
	    static createFrom(source: any = {}) {
	        return new GraphNode(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.instance = source["instance"];
	        this.kind = source["kind"];
	        this.type = source["type"];
	        this.typeHash = source["typeHash"];
	        this.named = source["named"];
	        this.short = source["short"];
	        this.display = source["display"];
	        this.comment = source["comment"];
	        this.debug = source["debug"];
	        this.x = source["x"];
	        this.y = source["y"];
	        this.uniqueId = source["uniqueId"];
	        this.stateIndex = source["stateIndex"];
	        this.remoteSync = source["remoteSync"];
	        this.scope = source["scope"];
	        this.clientOnly = source["clientOnly"];
	        this.serverOnly = source["serverOnly"];
	        this.net = source["net"];
	        this.netWhy = source["netWhy"];
	        this.netConflict = source["netConflict"];
	        this.plugs = this.convertValues(source["plugs"], Plug);
	        this.params = this.convertValues(source["params"], Param);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Graph {
	    nodes: GraphNode[];
	    links: GraphLink[];
	    containers: GraphContainer[];
	    states: number;
	    entries: number;
	    syncVars: number;
	    remoteSync: number;
	    unlinked: number;
	    layout?: GraphLayout;
	
	    static createFrom(source: any = {}) {
	        return new Graph(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.nodes = this.convertValues(source["nodes"], GraphNode);
	        this.links = this.convertValues(source["links"], GraphLink);
	        this.containers = this.convertValues(source["containers"], GraphContainer);
	        this.states = source["states"];
	        this.entries = source["entries"];
	        this.syncVars = source["syncVars"];
	        this.remoteSync = source["remoteSync"];
	        this.unlinked = source["unlinked"];
	        this.layout = this.convertValues(source["layout"], GraphLayout);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	
	
	
	export class Node {
	    path: string;
	    label: string;
	    fieldHash?: number;
	    kind: string;
	    type?: string;
	    typeHash?: number;
	    named: boolean;
	    value?: string;
	    alt?: string;
	    guid?: string;
	    refName?: string;
	    missing?: boolean;
	    instance: number;
	    count?: number;
	    children?: Node[];
	
	    static createFrom(source: any = {}) {
	        return new Node(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.label = source["label"];
	        this.fieldHash = source["fieldHash"];
	        this.kind = source["kind"];
	        this.type = source["type"];
	        this.typeHash = source["typeHash"];
	        this.named = source["named"];
	        this.value = source["value"];
	        this.alt = source["alt"];
	        this.guid = source["guid"];
	        this.refName = source["refName"];
	        this.missing = source["missing"];
	        this.instance = source["instance"];
	        this.count = source["count"];
	        this.children = this.convertValues(source["children"], Node);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	

}


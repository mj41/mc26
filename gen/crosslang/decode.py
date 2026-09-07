#!/usr/bin/env python3
"""
Schema-driven Minecraft 26.2 packet codec, written from the JSON alone.

Reads a capture of packet bodies, decodes each one against packet_schema.json
using only the node kinds documented in nodes.json and the primitives defined
in prims.json, then re-encodes the decoded value and checks the bytes match.

Nothing here special-cases a packet: the reader is driven by the node kind.

Usage:
  python3 decode.py --data <dir> --prims <prims.json> --nodes <nodes.json>
                    --capture <file.jsonl>
"""

import argparse
import json
import os
import struct
import sys
from collections import Counter, OrderedDict

sys.setrecursionlimit(30000)


# --------------------------------------------------------------------------
# errors
# --------------------------------------------------------------------------

class Hole(Exception):
    """The JSON does not describe this. Never guessed around, always raised."""


class WireError(Exception):
    """The bytes did not match what the schema says they should be."""


ABSENT = object()          # a field whose `when` was false


# --------------------------------------------------------------------------
# byte reader / writer
# --------------------------------------------------------------------------

class Reader:
    def __init__(self, buf):
        self.buf = buf
        self.pos = 0
        self.limits = [len(buf)]        # innermost frame end

    @property
    def limit(self):
        return self.limits[-1]

    def push_limit(self, n):
        end = self.pos + n
        if end > self.limit:
            raise WireError("length-prefixed window of %d runs past the end" % n)
        self.limits.append(end)

    def pop_limit(self):
        end = self.limits.pop()
        if self.pos != end:
            raise WireError("length-prefixed window: %d bytes left unread"
                            % (end - self.pos))

    def take(self, n):
        if n < 0:
            raise WireError("negative length %d" % n)
        if self.pos + n > self.limit:
            raise WireError("want %d bytes, %d left" % (n, self.limit - self.pos))
        b = self.buf[self.pos:self.pos + n]
        self.pos += n
        return b

    def u8(self):
        return self.take(1)[0]

    def rest(self):
        return self.take(self.limit - self.pos)


class Writer:
    def __init__(self):
        self.parts = []

    def raw(self, b):
        self.parts.append(bytes(b))

    def bytes(self):
        return b"".join(self.parts)

    def mark(self):
        return len(self.parts)

    def since(self, m):
        return b"".join(self.parts[m:])


# --------------------------------------------------------------------------
# the 18 natives prims.json names, and nothing else
#   each entry: (decode(reader, params) -> value, encode(writer, value, params))
# --------------------------------------------------------------------------

def _dec_varint(r, p):
    # prims.json VAR_INT: "unsigned LEB128 of the 32-bit two's-complement
    # pattern ... at most 5 bytes"
    v = 0
    for i in range(5):
        b = r.u8()
        v |= (b & 0x7F) << (7 * i)
        if not (b & 0x80):
            break
    else:
        raise WireError("var int longer than 5 bytes")
    v &= 0xFFFFFFFF
    return v - (1 << 32) if v & 0x80000000 else v


def _enc_varint(w, v, p):
    u = v & 0xFFFFFFFF
    out = bytearray()
    while True:
        b = u & 0x7F
        u >>= 7
        if u:
            out.append(b | 0x80)
        else:
            out.append(b)
            break
    w.raw(out)


def _dec_varlong(r, p):
    v = 0
    for i in range(10):
        b = r.u8()
        v |= (b & 0x7F) << (7 * i)
        if not (b & 0x80):
            break
    else:
        raise WireError("var long longer than 10 bytes")
    v &= 0xFFFFFFFFFFFFFFFF
    return v - (1 << 64) if v & 0x8000000000000000 else v


def _enc_varlong(w, v, p):
    u = v & 0xFFFFFFFFFFFFFFFF
    out = bytearray()
    while True:
        b = u & 0x7F
        u >>= 7
        if u:
            out.append(b | 0x80)
        else:
            out.append(b)
            break
    w.raw(out)


def _fixed(fmt, size):
    def dec(r, p):
        return struct.unpack(fmt, r.take(size))[0]

    def enc(w, v, p):
        w.raw(struct.pack(fmt, v))
    return dec, enc


def _dec_bool(r, p):
    # prims.json BOOL: 0 false, any non-zero true, the writer emits 1
    return r.u8() != 0


def _enc_bool(w, v, p):
    w.raw(b"\x01" if v else b"\x00")


def _dec_string(r, p):
    # prims.json STRING: "a var-int byte count, then exactly that many UTF-8
    # bytes".  Kept as bytes: the cap in `max` is a character cap and is
    # validation only, never on the wire.
    n = _dec_varint(r, None)
    if n < 0:
        raise WireError("negative string length %d" % n)
    return r.take(n)


def _enc_string(w, v, p):
    _enc_varint(w, len(v), None)
    w.raw(v)


def _dec_rest(r, p):
    return r.rest()


def _enc_rest(w, v, p):
    w.raw(v)


def _dec_fixed_bytes(r, p):
    if "len" not in p:
        raise Hole("FIXED_BYTES with no `len` on the node")
    return r.take(p["len"])


def _enc_fixed_bytes(w, v, p):
    w.raw(v)


def _dec_fixed_bit_set(r, p):
    if "bits" not in p:
        raise Hole("FIXED_BIT_SET with no `bits` on the node")
    return r.take((p["bits"] + 7) // 8)


def _dec_optional_var_int(r, p):
    # prims.json OPTIONAL_VAR_INT: 0 absent, n means n-1
    n = _dec_varint(r, None)
    return None if n == 0 else n - 1


def _enc_optional_var_int(w, v, p):
    _enc_varint(w, 0 if v is None else v + 1, None)


def _dec_lp_vec3(r, p):
    # prims.json LP_VEC3 describes only the READ direction; the packing is not
    # given, so the wire fields are kept verbatim and written back unchanged.
    b0 = r.u8()
    if b0 == 0:
        return {"b0": 0}
    b1 = r.u8()
    u = struct.unpack(">I", r.take(4))[0]
    out = {"b0": b0, "b1": b1, "u": u}
    if b0 & 4:
        out["h"] = _dec_varint(r, None)
    return out


def _enc_lp_vec3(w, v, p):
    w.raw(bytes([v["b0"]]))
    if v["b0"] == 0:
        return
    w.raw(bytes([v["b1"]]))
    w.raw(struct.pack(">I", v["u"]))
    if v["b0"] & 4:
        _enc_varint(w, v["h"], None)


def lp_vec3_value(v):
    """The (x, y, z) prims.json defines for an LP_VEC3, for reporting only."""
    if v["b0"] == 0:
        return (0.0, 0.0, 0.0)
    packed = (v["u"] << 16) | (v["b1"] << 8) | v["b0"]
    scale = v["b0"] & 3
    if v["b0"] & 4:
        scale |= (v["h"] & 0xFFFFFFFF) << 2

    def q(word):
        return min(word & 0x7FFF, 32766) * 2 / 32766 - 1
    return (q(packed >> 3) * scale, q(packed >> 18) * scale,
            q(packed >> 33) * scale)


NATIVES = {
    "bool":             (_dec_bool, _enc_bool),
    "i8":               _fixed(">b", 1),
    "u8":               _fixed(">B", 1),
    "i16be":            _fixed(">h", 2),
    "u16be":            _fixed(">H", 2),
    "i32be":            _fixed(">i", 4),
    "i64be":            _fixed(">q", 8),
    "f32be":            _fixed(">f", 4),
    "f64be":            _fixed(">d", 8),
    "varint":           (_dec_varint, _enc_varint),
    "varlong":          (_dec_varlong, _enc_varlong),
    "string":           (_dec_string, _enc_string),
    "rest_bytes":       (_dec_rest, _enc_rest),
    "fixed_bytes":      (_dec_fixed_bytes, _enc_fixed_bytes),
    "fixed_bit_set":    (_dec_fixed_bit_set, _enc_fixed_bytes),
    "optional_var_int": (_dec_optional_var_int, _enc_optional_var_int),
    "lp_vec3":          (_dec_lp_vec3, _enc_lp_vec3),
    # "nbt" is deliberately absent: see NBT below and FINDINGS.md
}


# --------------------------------------------------------------------------
# NBT.  prims.json's NBT definition carries a `tags` table: for every tag id,
# what its payload is.  This reader follows that table and nothing else, and
# refuses when the table is absent, which is what it did when the definition
# said only "a 1-byte tag id then that tag's payload" and stopped.
# --------------------------------------------------------------------------

NBT_USED = [0]


class NBTUnavailable(Hole):
    pass


def _nbt_str(r):
    n = struct.unpack(">H", r.take(2))[0]
    return r.take(n)


def _nbt_payload(r, tag):
    if tag == 1:
        return r.take(1)
    if tag == 2:
        return r.take(2)
    if tag == 3 or tag == 5:
        return r.take(4)
    if tag == 4 or tag == 6:
        return r.take(8)
    if tag == 7:
        n = struct.unpack(">i", r.take(4))[0]
        return ("ba", n, r.take(n))
    if tag == 8:
        return ("s", _nbt_str(r))
    if tag == 9:
        et = r.u8()
        n = struct.unpack(">i", r.take(4))[0]
        return ("l", et, n, [_nbt_payload(r, et) for _ in range(max(n, 0))])
    if tag == 10:
        items = []
        while True:
            t = r.u8()
            if t == 0:
                break
            nm = _nbt_str(r)
            items.append((t, nm, _nbt_payload(r, t)))
        return ("c", items)
    if tag == 11:
        n = struct.unpack(">i", r.take(4))[0]
        return ("ia", n, r.take(4 * n))
    if tag == 12:
        n = struct.unpack(">i", r.take(4))[0]
        return ("la", n, r.take(8 * n))
    raise WireError("unknown NBT tag id %d" % tag)


def _nbt_write_payload(w, tag, v):
    if tag in (1, 2, 3, 4, 5, 6):
        w.raw(v)
        return
    if tag == 7:
        w.raw(struct.pack(">i", v[1]))
        w.raw(v[2])
        return
    if tag == 8:
        w.raw(struct.pack(">H", len(v[1])))
        w.raw(v[1])
        return
    if tag == 9:
        w.raw(bytes([v[1]]))
        w.raw(struct.pack(">i", v[2]))
        for it in v[3]:
            _nbt_write_payload(w, v[1], it)
        return
    if tag == 10:
        for t, nm, pv in v[1]:
            w.raw(bytes([t]))
            w.raw(struct.pack(">H", len(nm)))
            w.raw(nm)
            _nbt_write_payload(w, t, pv)
        w.raw(b"\x00")
        return
    if tag in (11, 12):
        w.raw(struct.pack(">i", v[1]))
        w.raw(v[2])
        return
    raise WireError("unknown NBT tag id %d" % tag)


def dec_nbt(r, tags):
    if not tags:
        raise NBTUnavailable(
            "NBT: the definition gives no tags table, so the per-tag payload "
            "layouts are nowhere in the permitted JSON")
    NBT_USED[0] += 1
    tag = r.u8()
    if str(tag) not in tags:
        raise WireError("NBT tag id %d is not in the definition's tags table" % tag)
    if tag == 0:
        return (0, None)
    return (tag, _nbt_payload(r, tag))


def enc_nbt(w, v):
    tag, payload = v
    w.raw(bytes([tag]))
    if tag != 0:
        _nbt_write_payload(w, tag, payload)


# --------------------------------------------------------------------------
# schema bundle
# --------------------------------------------------------------------------

class Schema:
    def __init__(self, data_dir, prims_path, nodes_path):
        j = lambda p: json.load(open(p, encoding="utf-8"))
        self.packet_schema = j(os.path.join(data_dir, "packet_schema.json"))
        self.packets = j(os.path.join(data_dir, "packets.json"))
        self.registries = j(os.path.join(data_dir, "registries.json"))
        self.entity_data = j(os.path.join(data_dir, "entity_data.json"))
        self.prims = j(prims_path)["prims"]
        # The NBT definition carries the payload of every tag id; without it an
        # NBT value cannot be read past its first byte.
        self.nbt_tags = (self.prims.get("NBT", {}).get("def", {}) or {}).get("tags")
        self.nodes = j(nodes_path)

        self.documented_kinds = set(self.nodes["nodes"])

        # packet id -> name, per (state, flow)
        self.by_id = {}
        for state, flows in self.packets.items():
            for flow, names in flows.items():
                tbl = {}
                for name, info in names.items():
                    tbl[info["protocol_id"]] = name
                self.by_id[(state, flow)] = tbl

        # registry name -> {protocol_id: entry name} and the reverse
        self.reg_by_id = {}
        self.reg_by_name = {}
        for rname, r in self.registries.items():
            short = rname.split(":", 1)[1] if ":" in rname else rname
            fwd, rev = {}, {}
            for ename, e in r["entries"].items():
                fwd[e["protocol_id"]] = ename
                rev[ename] = e["protocol_id"]
            self.reg_by_id[short] = fwd
            self.reg_by_name[short] = rev

        # entity_data serializers, by id
        self.serializers = {s["id"]: s for s in self.entity_data["serializers"]}

    def packet_node(self, state, flow, pid):
        tbl = self.by_id.get((state, flow))
        if tbl is None:
            raise Hole("packets.json has no table for state %s / %s"
                       % (state, flow))
        name = tbl.get(pid)
        if name is None:
            raise Hole("no packet with id %d in %s/%s" % (pid, state, flow))
        entry = self.packet_schema["packets"].get(flow + "/" + name)
        if entry is None:
            raise Hole("packet_schema.json has no %s/%s" % (flow, name))
        return name, entry


# --------------------------------------------------------------------------
# conditions: `when` on a struct field, `while` on a whilelist
# --------------------------------------------------------------------------

def _field_value(struct_node, values, upto, name):
    """The nearest PRECEDING field with that name (names are not unique).

    A dotted name is one named run of bits of such a field: "flags.stepCount"
    is the field `flags`, whose node is a `bits`, cut at the offset and width
    that node gives for stepCount.
    """
    base, _, sub = name.partition(".")
    for i in range(upto - 1, -1, -1):
        if struct_node["fields"][i]["name"] == base:
            v = values[i]
            if v is ABSENT:
                raise Hole("`when` reads field %r, which was itself absent"
                           % name)
            fnode = struct_node["fields"][i]
            return _bit_field(v, fnode, sub) if sub else (v, fnode)
    raise Hole("`when` names field %r, which is not an earlier field" % name)


def _bit_field(v, fnode, sub):
    """One named run of bits of a packed integer (nodes.json `bits`)."""
    node = fnode["type"]
    if node.get("k") != "bits":
        raise Hole("field %r is not a packed integer, so it has no %r"
                   % (fnode["name"], sub))
    raw = (v[1] if isinstance(v, tuple) else v) & ((1 << node["bits"]) - 1)
    for f in node["fields"]:
        if f["name"] != sub:
            continue
        x = (raw >> f["offset"]) & ((1 << f["width"]) - 1)
        if f.get("signed") and x >> (f["width"] - 1):
            x -= 1 << f["width"]
        if f["width"] == 1 and not f.get("signed"):
            x = bool(x)
        return x, {"name": sub, "type": {"k": "prim", "t": node["of"]}}
    raise Hole("the packed %s has no field %r" % (node.get("name"), sub))


def _as_number(v, fnode, want):
    """Coerce a decoded field value to the number a `when` test compares."""
    if isinstance(v, bool):
        return 1 if v else 0
    if isinstance(v, int):
        return v
    raise Hole("`when` test on a %s field, which is not a number"
               % fnode["type"].get("k"))


def _eval_test(t, struct_node, values, upto):
    v, fnode = _field_value(struct_node, values, upto, t["field"])
    kind = t["test"]
    neg = bool(t.get("not"))

    if kind == "true":
        res = bool(v)
    elif kind == "bit":
        res = (_as_number(v, fnode, t) & t["value"]) != 0
    elif kind == "maskeq":
        res = (_as_number(v, fnode, t) & t["mask"]) == t["value"]
    elif kind == "eq":
        want = t["value"]
        if isinstance(want, str):
            # an enum constant compared by name: the wire value is the
            # position in the enum node's `values` (nodes.json, kind `enum`)
            en = fnode["type"]
            if en.get("k") != "enum" or "values" not in en:
                raise Hole("`when` eq %r on a field that is not a valued enum"
                           % want)
            if want not in en["values"]:
                raise Hole("`when` eq %r: not a constant of %s"
                           % (want, en.get("name")))
            at = en["values"].index(want)
            # nodes.json `enum`: the number on the wire is the ordinal unless the
            # node carries `ids`, and then it is the id of that constant
            want = en["ids"][at] if "ids" in en else at
        res = _as_number(v, fnode, t) == want
    elif kind == "cmp":
        n = _as_number(v, fnode, t)
        op = t["op"]
        res = {">": n > t["value"], ">=": n >= t["value"],
               "<": n < t["value"], "<=": n <= t["value"]}[op]
        if neg:                       # nodes.json: `not` inverts the operator
            return not res
        return res
    elif kind == "in":
        res = _as_number(v, fnode, t) in t["value"]
    else:
        raise Hole("unknown `when` test kind %r" % kind)
    return (not res) if neg else res


def field_present(field, struct_node, values, upto, ctx):
    w = field.get("when")
    if w is None:
        return True
    if isinstance(w, str):
        # nodes.json `guard`: the struct carries `guard`, the selector is an
        # EnumSet of that class carried EARLIER in the enclosing packet.
        g = struct_node.get("guard")
        if g is None:
            raise Hole("string `when` %r on a struct with no `guard`" % w)
        short = g.rsplit("/", 1)[-1]
        sel = ctx.lookup_enumset(short)
        if sel is None:
            raise Hole("guard %s: no enumset field with that name was decoded"
                       % short)
        return w in sel
    # list form: outer ANDed, inner ORed
    for group in w:
        if not any(_eval_test(t, struct_node, values, upto) for t in group):
            return False
    return True


# --------------------------------------------------------------------------
# the codec
# --------------------------------------------------------------------------

class Ctx:
    def __init__(self, schema, nbt_tags):
        self.s = schema
        self.allow_nbt = nbt_tags
        self.named = []        # [{name: node}] for `ref`
        self.enums = []        # [{enumset name: set(constants)}] for `guard`
        self.siblings = []     # [(struct_node, values)] for counted/lenprefixed
        self.path = []

    def lookup_enumset(self, short):
        for frame in reversed(self.enums):
            if short in frame:
                return frame[short]
        return None

    def lookup_ref(self, name, of):
        for frame in reversed(self.named):
            n = frame.get(name)
            if n is not None and n.get("k") == of:
                return n
        return None

    def where(self):
        return "/".join(self.path)


def resolve_prim(schema, t, params):
    """Follow prims.json until a native / bits / composed node is reached."""
    seen = []
    while True:
        if t in seen:
            raise Hole("primitive %s is defined in terms of itself" % t)
        seen.append(t)
        d = schema.prims.get(t)
        if d is None:
            raise Hole("primitive %s is not defined in prims.json" % t)
        node = d["def"]
        if node.get("k") == "prim":
            extra = {k: v for k, v in node.items() if k not in ("k", "t")}
            params = dict(params)
            params.update(extra)
            t = node["t"]
            continue
        return node, params


class Codec:
    def __init__(self, ctx):
        self.ctx = ctx
        self.s = ctx.s

    # ---------------------------------------------------------------- decode
    def decode(self, node, r, params=None):
        params = params or {}
        k = node.get("k")
        if k is None and {"id", "num", "type"} <= set(node):
            k = "case"
        if k not in self.s.documented_kinds and k not in ("case",):
            raise Hole("node kind %r is not described in nodes.json" % k)
        fn = getattr(self, "d_" + k, None)
        if fn is None:
            raise Hole("node kind %r has no reader" % k)
        return fn(node, r, params)

    def d_prim(self, node, r, params):
        p = dict(params)
        p.update({kk: vv for kk, vv in node.items() if kk not in ("k", "t")})
        target, p = resolve_prim(self.s, node["t"], p)
        if target.get("k") == "native":
            of = target["of"]
            if of == "nbt":
                return ("nbt", dec_nbt(r, self.ctx.allow_nbt))
            impl = NATIVES.get(of)
            if impl is None:
                raise Hole("native %r has no reader" % of)
            return impl[0](r, p)
        return self.decode(target, r, p)

    def d_bits(self, node, r, params):
        # nodes.json `bits`: exactly the bytes of the primitive named by `of`
        inner = self.decode({"k": "prim", "t": node["of"]}, r, {})
        return ("bits", inner)

    def d_unit(self, node, r, params):
        return None

    def d_struct(self, node, r, params):
        if node.get("conditional"):
            raise Hole("struct %s is marked conditional: the extractor could "
                       "not say which fields are on the wire"
                       % node.get("name"))
        values = []
        self.ctx.enums.append({})
        if node.get("name"):
            self.ctx.named.append({node["name"]: node})
        self.ctx.siblings.append((node, values))
        try:
            for i, f in enumerate(node["fields"]):
                if not field_present(f, node, values, i, self.ctx):
                    values.append(ABSENT)
                    continue
                self.ctx.path.append(f["name"])
                values.append(self.decode(f["type"], r))
                self.ctx.path.pop()
                if f["type"].get("k") == "enumset":
                    self.ctx.enums[-1][f["type"]["name"]] = values[-1][1]
        finally:
            self.ctx.siblings.pop()
            if node.get("name"):
                self.ctx.named.pop()
            self.ctx.enums.pop()
        return ("struct", values)

    def d_list(self, node, r, params):
        n = _dec_varint(r, None)
        if n < 0:
            raise WireError("negative list count %d" % n)
        out = []
        for i in range(n):
            self.ctx.path.append("[%d]" % i)
            out.append(self.decode(node["elem"], r))
            self.ctx.path.pop()
        return ("list", out)

    def d_counted(self, node, r, params):
        snode, values = self.ctx.siblings[-1]
        n, _ = _field_value(snode, values, len(values), node["count"])
        out = []
        for i in range(n):
            self.ctx.path.append("[%d]" % i)
            out.append(self.decode(node["elem"], r))
            self.ctx.path.pop()
        return ("counted", out)

    def d_lenprefixed(self, node, r, params):
        snode, values = self.ctx.siblings[-1]
        n, _ = _field_value(snode, values, len(values), node["length"])
        r.push_limit(n)
        v = self.decode(node["elem"], r)
        r.pop_limit()
        return ("lenprefixed", v)

    def d_map(self, node, r, params):
        n = _dec_varint(r, None)
        if n < 0:
            raise WireError("negative map count %d" % n)
        out = []
        for i in range(n):
            self.ctx.path.append("{%d}" % i)
            kk = self.decode(node["key"], r)
            vv = self.decode(node["val"], r)
            self.ctx.path.pop()
            out.append((kk, vv))
        return ("map", out)

    def d_optional(self, node, r, params):
        if node["elem"].get("k") == "nbt":
            raise Hole(
                "optional{nbt}: packet_schema.json says a boolean then a tag, "
                "but nodes.json says this construct (optionalTagCodec) has no "
                "boolean byte and sits inside an erased length prefix -- the "
                "two authorities contradict each other, so the bytes are "
                "unknowable from the JSON")
        present = r.u8() != 0
        return ("opt", present, self.decode(node["elem"], r) if present else None)

    def d_either(self, node, r, params):
        left = r.u8() != 0
        side = node["left"] if left else node["right"]
        return ("either", left, self.decode(side, r))

    def d_enum(self, node, r, params):
        if "values" not in node:
            raise Hole("enum %s has no `values` (java: %s): the constants are "
                       "not in the schema" % (node.get("name"), node.get("java")))
        if node.get("idsUnknown"):
            raise Hole("enum %s travels as an id of its own that the schema "
                       "could not read" % node.get("name"))
        return _dec_varint(r, None)

    def d_enumset(self, node, r, params):
        vals = node["values"]
        raw = r.take((len(vals) + 7) // 8)
        got = set()
        for i, name in enumerate(vals):
            if raw[i // 8] & (1 << (i % 8)):
                got.add(name)
        return ("enumset", got, raw)

    def d_stringenum(self, node, r, params):
        b = _dec_string(r, None)
        try:
            txt = b.decode("utf-8")
        except UnicodeDecodeError:
            raise WireError("stringenum: not UTF-8")
        if txt not in node["names"]:
            raise WireError("stringenum %s: %r is not one of %s"
                            % (node.get("name"), txt, node["names"]))
        return ("stringenum", txt)

    def d_string(self, node, r, params):
        return _dec_string(r, None)

    def d_registry(self, node, r, params):
        return _dec_varint(r, None)

    def d_resourcekey(self, node, r, params):
        return _dec_string(r, None)

    def d_holder(self, node, r, params):
        if "direct" in node:
            n = _dec_varint(r, None)
            if n == 0:
                return ("holder", 0, self.decode(node["direct"], r))
            return ("holder", n, None)
        return ("holder", _dec_varint(r, None), None)

    def d_holderset(self, node, r, params):
        c = _dec_varint(r, None)
        if c == 0:
            return ("holderset", _dec_string(r, None), None)
        if c < 0:
            raise WireError("negative holderset count %d" % c)
        return ("holderset", None, [_dec_varint(r, None) for _ in range(c - 1)])

    def d_ref(self, node, r, params):
        target = self.ctx.lookup_ref(node["name"], node["of"])
        if target is None:
            raise Hole("ref to %s (%s) has no enclosing node of that name"
                       % (node["name"], node["of"]))
        return ("ref", self.decode(target, r))

    def d_opaque(self, node, r, params):
        raise Hole("opaque node (%s): the extractor could not describe these "
                   "bytes" % node.get("java"))

    def d_nbt(self, node, r, params):
        return ("nbt", dec_nbt(r, self.ctx.allow_nbt))

    def d_case(self, node, r, params):
        return self.decode(node["type"], r)

    # -- dispatch ---------------------------------------------------------
    def _cases(self, node):
        """(lookup(key) -> case node, encode-side id of a case)."""
        cf = node.get("casesFrom")
        if cf == "packet_schema.json#components":
            reg = self.s.reg_by_id["data_component_type"]

            def by_key(kv):
                name = reg.get(kv)
                if name is None:
                    raise WireError("data_component_type id %d is in no "
                                    "registry entry" % kv)
                comp = self.s.packet_schema["components"].get(name)
                if comp is None:
                    raise Hole("component %s has no schema entry" % name)
                return comp["type"], name
            return by_key
        if cf == "entity_data.json#serializers":
            def by_key(kv):
                ser = self.s.serializers.get(kv)
                if ser is None:
                    raise WireError("entity data serializer id %d is not in "
                                    "entity_data.json" % kv)
                return ser["type"], ser["name"]
            return by_key
        if "cases" not in node:
            raise Hole("dispatch %s has no `cases`: the payloads are not in "
                       "the schema" % node.get("name"))
        key = node["key"]
        if key.get("k") == "registry":
            reg = self.s.reg_by_name.get(key["registry"])
            if reg is None:
                raise Hole("dispatch key registry %r is not in registries.json"
                           % key["registry"])
            table = {}
            for c in node["cases"]:
                pid = reg.get(c["id"])
                if pid is None:
                    raise Hole("case id %r is not an entry of registry %r"
                               % (c["id"], key["registry"]))
                table[pid] = c
        elif key.get("k") == "enum":
            table = {c["num"]: c for c in node["cases"]}
        else:
            raise Hole("dispatch key kind %r" % key.get("k"))

        def by_key(kv):
            c = table.get(kv)
            if c is None:
                raise WireError("dispatch %s: no case for key %d"
                                % (node.get("name"), kv))
            return c["type"], c["id"]
        return by_key

    def d_dispatch(self, node, r, params):
        key = node["key"]
        if node.get("name"):
            self.ctx.named.append({node["name"]: node})
        try:
            if "k" not in key and "field" in key:
                # prims.json DELIMITED_COMPONENT_PATCH: the key is an earlier
                # sibling field, already on the wire, not read again here
                snode, values = self.ctx.siblings[-1]
                kv, _ = _field_value(snode, values, len(values), key["field"])
                inline_key = False
            elif key.get("k") == "either":
                raise Hole("dispatch %s has an `either` key and no cases"
                           % node.get("name"))
            elif key.get("k") == "enum" and ("ids" in key or key.get("idsUnknown")):
                # the cases are numbered by ordinal, which is then the wrong number
                raise Hole("dispatch %s is keyed by an enum that does not "
                           "travel as its ordinal" % node.get("name"))
            else:
                kv = self.decode(key, r)
                inline_key = True
            by_key = self._cases(node)
            cnode, cid = by_key(kv)
            self.ctx.path.append(str(cid))
            v = self.decode(cnode, r)
            self.ctx.path.pop()
            return ("dispatch", kv, v, inline_key)
        finally:
            if node.get("name"):
                self.ctx.named.pop()

    def d_whilelist(self, node, r, params):
        cond = node["while"]
        out = []
        while True:
            self.ctx.path.append("[%d]" % len(out))
            e = self.decode(node["elem"], r)
            self.ctx.path.pop()
            out.append(e)
            if self._while_done(node, e):
                break
            if r.pos >= r.limit:
                raise WireError("whilelist ran off the end without its "
                                "terminating entry")
        return ("whilelist", out)

    def _while_done(self, node, entry):
        cond = node["while"]
        elem = node["elem"]
        if elem.get("k") != "struct":
            raise Hole("whilelist `while` on a non-struct element")
        if cond.get("field", "") == "":
            raise Hole("whilelist `while` with an empty `field`: the tested "
                       "value is the entry itself, which the schema cannot name")
        values = entry[1]
        return _eval_test(cond, elem, values, len(values))

    # ---------------------------------------------------------------- encode
    def encode(self, node, v, w, params=None):
        params = params or {}
        k = node.get("k")
        if k is None and {"id", "num", "type"} <= set(node):
            k = "case"
        fn = getattr(self, "e_" + k, None)
        if fn is None:
            raise Hole("node kind %r has no writer" % k)
        return fn(node, v, w, params)

    def e_prim(self, node, v, w, params):
        p = dict(params)
        p.update({kk: vv for kk, vv in node.items() if kk not in ("k", "t")})
        target, p = resolve_prim(self.s, node["t"], p)
        if target.get("k") == "native":
            of = target["of"]
            if of == "nbt":
                return enc_nbt(w, v[1])
            NATIVES[of][1](w, v, p)
            return
        return self.encode(target, v, w, p)

    def e_bits(self, node, v, w, params):
        self.encode({"k": "prim", "t": node["of"]}, v[1], w, {})

    def e_unit(self, node, v, w, params):
        return

    def e_struct(self, node, v, w, params):
        values = v[1]
        self.ctx.enums.append({})
        self.ctx.siblings.append((node, values))
        try:
            for i, f in enumerate(node["fields"]):
                present = field_present(f, node, values, i, self.ctx)
                if (values[i] is ABSENT) != (not present):
                    raise WireError("field %s: `when` disagrees between the "
                                    "read and the write" % f["name"])
                if not present:
                    continue
                self.encode(f["type"], values[i], w)
                if f["type"].get("k") == "enumset":
                    self.ctx.enums[-1][f["type"]["name"]] = values[i][1]
        finally:
            self.ctx.siblings.pop()
            self.ctx.enums.pop()

    def e_list(self, node, v, w, params):
        _enc_varint(w, len(v[1]), None)
        for it in v[1]:
            self.encode(node["elem"], it, w)

    def e_counted(self, node, v, w, params):
        snode, values = self.ctx.siblings[-1]
        n, _ = _field_value(snode, values, len(values), node["count"])
        if n != len(v[1]):
            raise WireError("counted: %d entries but the count field says %d"
                            % (len(v[1]), n))
        for it in v[1]:
            self.encode(node["elem"], it, w)

    def e_lenprefixed(self, node, v, w, params):
        snode, values = self.ctx.siblings[-1]
        n, _ = _field_value(snode, values, len(values), node["length"])
        m = w.mark()
        self.encode(node["elem"], v[1], w)
        got = len(w.since(m))
        if got != n:
            raise WireError("lenprefixed: re-encoded to %d bytes, the length "
                            "field says %d" % (got, n))

    def e_map(self, node, v, w, params):
        _enc_varint(w, len(v[1]), None)
        for kk, vv in v[1]:
            self.encode(node["key"], kk, w)
            self.encode(node["val"], vv, w)

    def e_optional(self, node, v, w, params):
        if node["elem"].get("k") == "nbt":
            raise Hole("optional{nbt}: see the reader")
        w.raw(b"\x01" if v[1] else b"\x00")
        if v[1]:
            self.encode(node["elem"], v[2], w)

    def e_either(self, node, v, w, params):
        w.raw(b"\x01" if v[1] else b"\x00")
        self.encode(node["left"] if v[1] else node["right"], v[2], w)

    def e_enum(self, node, v, w, params):
        _enc_varint(w, v, None)

    def e_enumset(self, node, v, w, params):
        vals = node["values"]
        raw = bytearray((len(vals) + 7) // 8)
        for i, name in enumerate(vals):
            if name in v[1]:
                raw[i // 8] |= 1 << (i % 8)
        w.raw(raw)

    def e_stringenum(self, node, v, w, params):
        _enc_string(w, v[1].encode("utf-8"), None)

    def e_string(self, node, v, w, params):
        _enc_string(w, v, None)

    def e_registry(self, node, v, w, params):
        _enc_varint(w, v, None)

    def e_resourcekey(self, node, v, w, params):
        _enc_string(w, v, None)

    def e_holder(self, node, v, w, params):
        _enc_varint(w, v[1], None)
        if "direct" in node and v[1] == 0:
            self.encode(node["direct"], v[2], w)

    def e_holderset(self, node, v, w, params):
        if v[2] is None:
            _enc_varint(w, 0, None)
            _enc_string(w, v[1], None)
        else:
            _enc_varint(w, len(v[2]) + 1, None)
            for i in v[2]:
                _enc_varint(w, i, None)

    def e_ref(self, node, v, w, params):
        target = self.ctx.lookup_ref(node["name"], node["of"])
        if target is None:
            raise Hole("ref to %s has no enclosing node" % node["name"])
        self.encode(target, v[1], w)

    def e_opaque(self, node, v, w, params):
        raise Hole("opaque node")

    def e_nbt(self, node, v, w, params):
        enc_nbt(w, v[1])

    def e_case(self, node, v, w, params):
        self.encode(node["type"], v, w)

    def e_dispatch(self, node, v, w, params):
        if node.get("name"):
            self.ctx.named.append({node["name"]: node})
        try:
            _, kv, inner, inline_key = v
            if inline_key:
                self.encode(node["key"], kv, w)
            by_key = self._cases(node)
            cnode, _cid = by_key(kv)
            self.encode(cnode, inner, w)
        finally:
            if node.get("name"):
                self.ctx.named.pop()

    def e_whilelist(self, node, v, w, params):
        for e in v[1]:
            self.encode(node["elem"], e, w)


# --------------------------------------------------------------------------
# reporting
# --------------------------------------------------------------------------

def summarise(v, depth=0):
    if v is ABSENT:
        return "<absent>"
    if isinstance(v, bytes):
        return "0x" + v[:16].hex() + ("..." if len(v) > 16 else "")
    if isinstance(v, tuple) and v and isinstance(v[0], str):
        tag = v[0]
        if tag == "struct":
            if depth > 3:
                return "{...}"
            return "{" + ", ".join(summarise(x, depth + 1) for x in v[1]) + "}"
        if tag in ("list", "counted", "whilelist"):
            return "[" + ", ".join(summarise(x, depth + 1) for x in v[1][:4]) + \
                   ("..." if len(v[1]) > 4 else "") + "]"
        return tag + "(" + ", ".join(summarise(x, depth + 1) for x in v[1:2]) + ")"
    return repr(v)


def first_diff(a, b):
    n = min(len(a), len(b))
    for i in range(n):
        if a[i] != b[i]:
            return i
    return n if len(a) != len(b) else -1


# --------------------------------------------------------------------------
# main
# --------------------------------------------------------------------------

def run(args):
    schema = Schema(args.data, args.prims, args.nodes)

    total = 0
    decoded = 0
    matched = 0
    failures = Counter()
    examples = {}
    nbt_packets = 0

    with open(args.capture, encoding="utf-8") as fh:
        for lineno, line in enumerate(fh, 1):
            line = line.strip()
            if not line:
                continue
            total += 1
            rec = json.loads(line)
            state, flow, pid = rec["state"], rec["flow"], rec["id"]
            body = bytes.fromhex(rec["data"])
            name = "%s/%s id %d" % (state, flow, pid)

            def fail(what):
                failures[(name, what)] += 1
                examples.setdefault((name, what), lineno)

            try:
                name, entry = schema.packet_node(state, flow, pid)
                name = "%s/%s" % (flow, name)
            except Hole as e:
                fail("no schema: %s" % e)
                continue

            ctx = Ctx(schema, schema.nbt_tags)
            codec = Codec(ctx)
            r = Reader(body)
            nbt_before = NBT_USED[0]
            try:
                value = codec.decode(entry["type"], r)
                if r.pos != len(body):
                    raise WireError("%d of %d bytes consumed, %d left over"
                                    % (r.pos, len(body), len(body) - r.pos))
            except Hole as e:
                fail("undescribed: %s%s" % (e, " at " + ctx.where()
                                            if ctx.where() else ""))
                continue
            except WireError as e:
                fail("decode: %s%s" % (e, " at " + ctx.where()
                                       if ctx.where() else ""))
                continue
            except Exception as e:
                fail("decode: %s: %s" % (type(e).__name__, e))
                continue

            decoded += 1
            if NBT_USED[0] > nbt_before:
                nbt_packets += 1

            ctx2 = Ctx(schema, schema.nbt_tags)
            w = Writer()
            try:
                Codec(ctx2).encode(entry["type"], value, w)
                out = w.bytes()
            except Exception as e:
                fail("encode: %s: %s" % (type(e).__name__, e))
                continue

            if out == body:
                matched += 1
                if args.verbose:
                    print("ok   %-45s %s" % (name, summarise(value)))
            else:
                i = first_diff(body, out)
                fail("re-encoded differently at byte %d (in %d, out %d)"
                     % (i, len(body), len(out)))
                if args.verbose:
                    print("DIFF %s\n  in  %s\n  out %s" % (name, body.hex(),
                                                           out.hex()))

    print("decoded %d of %d packets, %d round-tripped byte for byte"
          % (decoded, total, matched))
    if nbt_packets:
        print("(%d of those %d contained NBT, read from the tags table of the "
              "NBT definition)" % (nbt_packets, decoded))
    if failures:
        print()
        for (name, what), n in failures.most_common():
            print("%-42s %-90s x%d  (first: line %d)"
                  % (name, what, n, examples[(name, what)]))
    return 0 if (failures == Counter() and total > 0) else 1


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--data", required=True)
    ap.add_argument("--prims", required=True)
    ap.add_argument("--nodes", required=True)
    ap.add_argument("--capture", required=True)
    ap.add_argument("--verbose", action="store_true")
    args = ap.parse_args()
    sys.exit(run(args))


if __name__ == "__main__":
    main()

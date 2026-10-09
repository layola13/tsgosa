function f(s: string | null): i32 {
  if (s === null) {
    return 0;
  }
  console.log(s);
  return 1;
}
function g(s: string | null): i32 {
  if (s !== null) {
    console.log(s);
    return 1;
  }
  return 0;
}
function h(s: string | undefined): i32 {
  if (null === s) {
    return 0;
  }
  if (s === undefined) {
    return 0;
  }
  console.log(s);
  return 1;
}
function fi(x: i32 | null): i32 {
  if (x === null) {
    return -1;
  }
  return x + 1;
}
interface P {
  x: i32;
}
function fp(p: P | null): i32 {
  if (p === null) {
    return -1;
  }
  return p.x;
}
function main(): i32 {
  console.log(f(null));
  console.log(f("a"));
  console.log(g(null));
  console.log(g("b"));
  console.log(h(undefined));
  console.log(h("c"));
  console.log(fi(null));
  console.log(fi(41));
  console.log(fp(null));
  const m = new Map<string, i32>();
  m.set("a", 5);
  const v = m.get("b");
  if (v === undefined) {
    console.log(-1);
  } else {
    console.log(v);
  }
  return 0;
}

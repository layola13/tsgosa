interface P {
  x: i32;
}
function mk(): P {
  return { x: 41 };
}
function use(p: P): i32 {
  return p.x + 1;
}
class B {
  v: i32 = 0;
  get(): i32 { return this.v + 1; }
}
class D extends B {
}
function mkd(): D {
  const d = new D();
  d.v = 41;
  return d;
}
function useB(p: B): i32 {
  return p.get();
}
function main(): i32 {
  console.log(use(mk()));
  console.log(useB(mkd()));
  return 0;
}

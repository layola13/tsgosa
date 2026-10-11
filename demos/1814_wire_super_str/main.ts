class B { s: string = ""; n: i32 = 0; constructor(t: string) { this.s = t; this.n = t.length; } }
class D extends B { m: i32 = 0; constructor(t: string) { super(t); this.m = t.length + 1; } }
function main(): i32 {
  const d = new D("hey");
  console.log(d.s);
  console.log(d.n + d.m);
  return 0;
}

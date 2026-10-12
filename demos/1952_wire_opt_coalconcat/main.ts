class C {
  s: string = "";
  constructor(t: string|null) { this.s = (t ?? "d") + "!"; }
}
function main(): i32 {
  const c = new C("hey");
  console.log(c.s);
  const d = new C(null);
  console.log(d.s);
  return 0;
}

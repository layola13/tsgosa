class C {
  s: string = "";
  constructor(t: string|null) { this.s = t?.foo ?? "?"; }
}
function main(): i32 {
  const c = new C("hey");
  console.log(c.s);
  return 0;
}

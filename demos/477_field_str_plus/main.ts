class C {
  s: string = "a";
}
function m(c: C): void {
  c.s += "b";
}
function main(): i32 {
  const c = new C();
  m(c);
  console.log(c.s);
  return 0;
}

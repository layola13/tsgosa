class C { #v: i32 = 8; get(): i32 { return this.#v; } }
function main(): i32 {
  console.log(new C().get());
  return 0;
}

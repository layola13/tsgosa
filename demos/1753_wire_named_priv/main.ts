class F { #n: i32 = 0; get(): i32 { return this.#n; } constructor(k: i32) { this.#n = k - 1; } }
function main(): i32 {
  const f = new F(8);
  console.log(f.get());
  return 0;
}

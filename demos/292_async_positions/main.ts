class C {
  v: i32 = 6;
  async get(): Promise<i32> { return this.v; }
}
const f = async () => 3;
async function get(): Promise<i32> { return 5; }
function add(a: i32, b: i32): i32 { return a + b; }
function main(): i32 {
  const c = new C();
  const m: i32 = await c.get();
  const t: i32 = await f();
  const v: i32 = add(await get(), 1);
  let r = 0;
  if (await get()) { r = 1; }
  console.log(m + t + v + r);
  return m + t + v + r;
}
console.log(main());

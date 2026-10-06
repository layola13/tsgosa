function main(): i32 {
  const f = async (x: i32) => x + 1;
  const g = async (): Promise<i32> => 4;
  const v: i32 = await f(1);
  const u: i32 = await g();
  console.log(v + u);
  return v + u;
}
console.log(main());

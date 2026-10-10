function main(): i32 {
  const a: i32[] = [1, 2, 3, 4];
  console.log(a.reduce((s: i32, v: i32) => s + v, 0));
  console.log(a.filter((v: i32) => v % 2 === 0).length);
  return 0;
}

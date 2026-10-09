function main(): i32 {
  const a: i32[] | null = [7, 8];
  console.log(a?.[1]);
  const n: i32[] | null = null;
  console.log(n?.[0]);
  let r: i32[] | null = null;
  r = [3, 4];
  console.log(r?.[0]);
  return 0;
}

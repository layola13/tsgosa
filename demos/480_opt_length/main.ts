function main(): i32 {
  const a: i32[] | null = null;
  console.log(a?.length);
  let b: i32[] | null = null;
  b = [7, 8, 9];
  console.log(b?.length);
  console.log(b.length);
  console.log(b[1]);
  return 0;
}

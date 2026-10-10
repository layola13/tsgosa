function main(): i32 {
  const a: i32[] = [1, 2, 3, 4];
  const s: string = a.slice(1, 3).join("-");
  console.log(s);
  return 0;
}

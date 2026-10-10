function main(): i32 {
  const w: string = "dog";
  const r = w == "cat" ? 1 : w == "dog" ? 2 : 3;
  console.log(r);
  return 0;
}

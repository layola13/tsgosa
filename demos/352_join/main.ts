function main(): i32 {
  const a: number[] = [1, 2, 3];
  console.log(a.join(",").length);
  console.log(a.join("-").length);
  console.log(a.join().length);
  const e: number[] = [];
  console.log(e.join(",").length);
  return 0;
}

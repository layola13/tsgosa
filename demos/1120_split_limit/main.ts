function main(): i32 {
  console.log("a,b,c".split(",", 0).length);
  console.log("a,b,c".split(",", 2).length);
  console.log("a,b,c".split(",")[5] ?? "oob");
  return 0;
}

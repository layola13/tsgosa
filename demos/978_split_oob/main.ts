function main(): i32 {
  const s: string = "a,b";
  console.log(s.split(",").length);
  console.log(s.split(",")[5] ?? "oob");
  console.log("".split(",").length);
  return 0;
}

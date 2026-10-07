function main(): i32 {
  const a: string[] = ["x", "yy", "z"];
  console.log(a.includes("yy"));
  console.log(a.includes("qq"));
  console.log(a.indexOf("z"));
  console.log(a.indexOf("qq"));
  console.log(a.includes("yy", 2));
  return 0;
}

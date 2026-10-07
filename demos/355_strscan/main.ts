function main(): i32 {
  const a: string[] = ["x", "yy", "x"];
  console.log(a.includes("yy"));
  console.log(a.includes("qq"));
  console.log(a.indexOf("x"));
  console.log(a.indexOf("qq"));
  console.log(a.includes("yy", 2));
  console.log(a.lastIndexOf("x"));
  console.log(a.lastIndexOf("qq"));
  console.log(a.lastIndexOf("x", 1));
  return 0;
}

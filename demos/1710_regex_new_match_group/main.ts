function main(): i32 {
  const re = new RegExp("(a+)(b+)");
  console.log("xxaabbyy".match(re)[0]);
  return 0;
}

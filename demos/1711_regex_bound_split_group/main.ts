function main(): i32 {
  const re = /(a+)/;
  console.log("aabbaa".split(re)[1]);
  return 0;
}

function main(): i32 {
  const re = /,/;
  console.log("a,b,c".split(re)[0]);
  console.log("a,b,c".split(re)[2]);
  return 0;
}

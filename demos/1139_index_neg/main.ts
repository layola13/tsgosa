function main(): i32 {
  console.log("abca".indexOf("a", -5));
  console.log("abca".indexOf("c", -1));
  console.log("abca".indexOf("a", 2));
  console.log("abca".lastIndexOf("a", -1));
  console.log("abca".lastIndexOf("a", 2));
  return 0;
}
